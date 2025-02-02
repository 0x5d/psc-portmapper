package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/0x5d/psc-portmapper/internal/gcp"
	"github.com/0x5d/psc-portmapper/internal/gcp/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type state struct {
	project string
	region  string
	spec    *Spec
	nodes   *corev1.NodeList
	sts     *appsv1.StatefulSet
	pods    *corev1.PodList
}

func (s *state) portMappings() []*gcp.PortMapping {
	numPods := len(s.pods.Items)
	mappings := make([]*gcp.PortMapping, 0, numPods)
	for i := 0; i < numPods; i++ {
		for _, p := range s.spec.NodePorts {
			port := p.StartingPort + int32(i)
			node := s.nodes.Items[i]
			instance, _ := fqInstaceName(node.Spec.ProviderID)
			mappings = append(mappings, &gcp.PortMapping{
				Port:         port,
				Instance:     instance,
				InstancePort: p.NodePort,
			})
		}
	}
	return mappings
}

func initialState() *state {
	zones := []string{"us-east1-a", "us-east1-a", "us-east1-a"}
	namespace := "default"
	project := "my-project"
	app := "my-app"
	n := len(zones)

	spec := &Spec{
		Prefix:        "prefix-",
		NatSubnetFQNs: []string{fmt.Sprintf("projects/%s/regions/us-east1/subnetworks/my-subnet", project)},
		NodePorts: map[string]PortConfig{
			"app": {NodePort: 30000, ContainerPort: 8080, StartingPort: 30000},
		},
	}
	specStr, _ := json.Marshal(spec)

	// Nodes
	nodes := make([]corev1.Node, 0, n)
	for i := 0; i < n; i++ {
		nodeName := fmt.Sprintf("node-%d", i)
		nodes = append(nodes, corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:        nodeName,
				Labels:      map[string]string{},
				Annotations: map[string]string{hostnameAnnotation: nodeName},
			},
			Spec: corev1.NodeSpec{
				ProviderID: fmt.Sprintf("gce://%s/%s/%s", project, zones[i], nodeName),
			},
		})
	}

	// StatefulSet
	stsName := "sts"
	replicas := int32(n)
	selector := &metav1.LabelSelector{
		MatchLabels: map[string]string{"app": app},
	}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   namespace,
			Name:        stsName,
			Annotations: map[string]string{annotation: string(specStr)},
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: selector,
			Replicas: &replicas,
		},
	}

	// Pods
	pods := make([]corev1.Pod, 0, n)
	containerPort := int32(8080)
	for i := 0; i < n; i++ {
		pods = append(pods, corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      fmt.Sprintf("pod-%d", i),
				Labels:    selector.MatchLabels,
			},
			Spec: corev1.PodSpec{
				NodeName: nodes[i].Name,
				Containers: []corev1.Container{{
					Name:  app,
					Image: app,
					Ports: []corev1.ContainerPort{{ContainerPort: containerPort}},
				}},
			},
		})
	}

	return &state{
		project: "my-project",
		region:  "us-east1",
		spec:    spec,
		nodes:   &corev1.NodeList{Items: nodes},
		sts:     sts,
		pods:    &corev1.PodList{Items: pods},
	}
}

func TestGetObsoletePortMappings(t *testing.T) {
	tests := []struct {
		name     string
		expected []*gcp.PortMapping
		actual   []*gcp.PortMapping
		want     []*gcp.PortMapping
	}{
		{
			name:     "No obsolete port mappings",
			expected: []*gcp.PortMapping{{Port: 80, Instance: "instance1", InstancePort: 8080}, {Port: 443, Instance: "instance2", InstancePort: 8443}},
			actual:   []*gcp.PortMapping{{Port: 80, Instance: "instance1", InstancePort: 8080}, {Port: 443, Instance: "instance2", InstancePort: 8443}},
			want:     nil,
		},
		{
			name:     "One obsolete port mapping",
			expected: []*gcp.PortMapping{{Port: 80, Instance: "instance1", InstancePort: 8080}},
			actual:   []*gcp.PortMapping{{Port: 80, Instance: "instance1", InstancePort: 8080}, {Port: 443, Instance: "instance2", InstancePort: 8443}},
			want:     []*gcp.PortMapping{{Port: 443, Instance: "instance2", InstancePort: 8443}},
		},
		{
			name:     "Multiple obsolete port mappings",
			expected: []*gcp.PortMapping{{Port: 80, Instance: "instance1", InstancePort: 8080}},
			actual:   []*gcp.PortMapping{{Port: 80, Instance: "instance1", InstancePort: 8080}, {Port: 443, Instance: "instance2", InstancePort: 8443}, {Port: 8080, Instance: "instance3", InstancePort: 8081}},
			want:     []*gcp.PortMapping{{Port: 443, Instance: "instance2", InstancePort: 8443}, {Port: 8080, Instance: "instance3", InstancePort: 8081}},
		},
		{
			name:     "All port mappings are obsolete",
			expected: []*gcp.PortMapping{{Port: 80, Instance: "instance3", InstancePort: 8080}, {Port: 443, Instance: "instance4", InstancePort: 8443}},
			actual:   []*gcp.PortMapping{{Port: 80, Instance: "instance1", InstancePort: 8080}, {Port: 443, Instance: "instance2", InstancePort: 8443}},
			want:     []*gcp.PortMapping{{Port: 80, Instance: "instance1", InstancePort: 8080}, {Port: 443, Instance: "instance2", InstancePort: 8443}},
		},
		{
			name:     "No actual port mappings",
			expected: []*gcp.PortMapping{{Port: 80, Instance: "instance1", InstancePort: 8080}, {Port: 443, Instance: "instance2", InstancePort: 8443}},
			actual:   []*gcp.PortMapping{},
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getObsoletePortMappings(tt.expected, tt.actual)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestReconcile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := "prefix-"
	fw := firewallName(p)
	neg := negName(p)
	be := backendName(p)
	fwdRule := fwdRuleName(p)
	svcAtt := svcAttName(p)
	tcp := "tcp"
	mctx := gomock.Any()

	tests := []struct {
		name           string
		state          func() *state
		setup          func(t *testing.T, mock *mock.MockClient, s *state)
		assert         func(t *testing.T, c client.Client, s *state)
		expectedRes    reconcile.Result
		expectedErrMsg string
	}{{
		name: "Creates everything",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			fwdRuleFQN := gcp.ForwardingRuleFQN(s.project, s.region, fwdRule)
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			instances := make([]string, 0, len(s.nodes.Items))
			for _, node := range s.nodes.Items {
				instances = append(instances, node.Name)
			}
			consumers := toConsumerProjectLimits(s.spec.ConsumerAcceptList)

			notFound(m.GetFirewallPolicies(mctx, fw))
			noErr(m.CreateFirewallPolicies(mctx, fw, ports, gomock.InAnyOrder(instances)))

			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))

			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))

			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))

			notFound(m.GetForwardingRule(mctx, fwdRule))
			noErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil, ports))

			notFound(m.GetServiceAttachment(mctx, svcAtt))
			noErr(m.CreateServiceAttachment(mctx, svcAtt, fwdRuleFQN, consumers, s.spec.NatSubnetFQNs))
		},
		assert: func(t *testing.T, c client.Client, s *state) {
			// Check that the nodeport was created too.
			nodeport := &corev1.Service{}
			err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: nodeportName(p)}, nodeport)
			require.NoError(t, err)

			require.Equal(t, nodeport.Labels, map[string]string{managedByLabel: portmapperApp})
		},
	}, {
		name: "Fails if it can't get the firewall",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			getErr(mock.EXPECT().GetFirewallPolicies(mctx, fw), errors.New("can't get firewall"))
		},
		expectedErrMsg: "can't get firewall",
	}, {
		name: "Fails if it can't create the firewall",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			instances := make([]string, 0, len(s.nodes.Items))
			for _, node := range s.nodes.Items {
				instances = append(instances, node.Name)
			}
			notFound(m.GetFirewallPolicies(mctx, fw))
			callErr(m.CreateFirewallPolicies(mctx, fw, ports, gomock.InAnyOrder(instances)), errors.New("can't create firewall"))
		},
		expectedErrMsg: "can't create firewall",
	}, {
		name: "Fails if it can't get the neg",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			instances := make([]string, 0, len(s.nodes.Items))
			for _, node := range s.nodes.Items {
				instances = append(instances, node.Name)
			}
			notFound(m.GetFirewallPolicies(mctx, fw))
			noErr(m.CreateFirewallPolicies(mctx, fw, ports, gomock.InAnyOrder(instances)))
			getErr(m.GetNEG(mctx, neg), errors.New("can't get NEG"))
		},
		expectedErrMsg: "can't get NEG",
	}, {
		name: "Fails if it can't create the neg",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			instances := make([]string, 0, len(s.nodes.Items))
			for _, node := range s.nodes.Items {
				instances = append(instances, node.Name)
			}
			notFound(m.GetFirewallPolicies(mctx, fw))
			noErr(m.CreateFirewallPolicies(mctx, fw, ports, gomock.InAnyOrder(instances)))
			notFound(m.GetNEG(mctx, neg))
			once(m.CreatePortmapNEG(mctx, neg)).Return(errors.New("can't create NEG"))
		},
		expectedErrMsg: "can't create NEG",
	}, {
		name: "Fails if it can't get the backend",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			instances := make([]string, 0, len(s.nodes.Items))
			for _, node := range s.nodes.Items {
				instances = append(instances, node.Name)
			}
			notFound(m.GetFirewallPolicies(mctx, fw))
			noErr(m.CreateFirewallPolicies(mctx, fw, ports, gomock.InAnyOrder(instances)))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			getErr(m.GetBackendService(mctx, be), errors.New("can't get backend"))
		},
		expectedErrMsg: "can't get backend",
	}, {
		name: "Fails if it can't create the backend",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			instances := make([]string, 0, len(s.nodes.Items))
			for _, node := range s.nodes.Items {
				instances = append(instances, node.Name)
			}
			notFound(m.GetFirewallPolicies(mctx, fw))
			noErr(m.CreateFirewallPolicies(mctx, fw, ports, gomock.InAnyOrder(instances)))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			callErr(m.CreateBackendService(mctx, be, neg), errors.New("can't create backend"))
		},
		expectedErrMsg: "can't create backend",
	}, {
		name: "Fails if it can't list the endpoints",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			instances := make([]string, 0, len(s.nodes.Items))
			for _, node := range s.nodes.Items {
				instances = append(instances, node.Name)
			}
			notFound(m.GetFirewallPolicies(mctx, fw))
			noErr(m.CreateFirewallPolicies(mctx, fw, ports, gomock.InAnyOrder(instances)))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return(nil, errors.New("can't list endpoints"))
		},
		expectedErrMsg: "can't list endpoints",
	}, {
		name: "Fails if it can't attach the endpoints",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			instances := make([]string, 0, len(s.nodes.Items))
			for _, node := range s.nodes.Items {
				instances = append(instances, node.Name)
			}
			notFound(m.GetFirewallPolicies(mctx, fw))
			noErr(m.CreateFirewallPolicies(mctx, fw, ports, gomock.InAnyOrder(instances)))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			callErr(m.AttachEndpoints(mctx, neg, s.portMappings()), errors.New("can't attach endpoints"))
		},
		expectedErrMsg: "can't attach endpoints",
	}, {
		name: "Fails if it can't get the forwarding rule",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			instances := make([]string, 0, len(s.nodes.Items))
			for _, node := range s.nodes.Items {
				instances = append(instances, node.Name)
			}
			notFound(m.GetFirewallPolicies(mctx, fw))
			noErr(m.CreateFirewallPolicies(mctx, fw, ports, gomock.InAnyOrder(instances)))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
			getErr(m.GetForwardingRule(mctx, fwdRule), errors.New("can't get forwarding rule"))
		},
		expectedErrMsg: "can't get forwarding rule",
	}, {
		name: "Fails if it can't create the forwarding rule",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			instances := make([]string, 0, len(s.nodes.Items))
			for _, node := range s.nodes.Items {
				instances = append(instances, node.Name)
			}
			notFound(m.GetFirewallPolicies(mctx, fw))
			noErr(m.CreateFirewallPolicies(mctx, fw, ports, gomock.InAnyOrder(instances)))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
			notFound(m.GetForwardingRule(mctx, fwdRule))
			callErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil, ports), errors.New("can't create forwarding rule"))
		},
		expectedErrMsg: "can't create forwarding rule",
	}, {
		name: "Fails if it can't get the service attachment",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			instances := make([]string, 0, len(s.nodes.Items))
			for _, node := range s.nodes.Items {
				instances = append(instances, node.Name)
			}
			notFound(m.GetFirewallPolicies(mctx, fw))
			noErr(m.CreateFirewallPolicies(mctx, fw, ports, gomock.InAnyOrder(instances)))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
			notFound(m.GetForwardingRule(mctx, fwdRule))
			noErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil, ports))
			getErr(m.GetServiceAttachment(mctx, svcAtt), errors.New("can't get service attachment"))
		},
		expectedErrMsg: "can't get service attachment",
	}, {
		name: "Fails if it can't create the service attachment",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			instances := make([]string, 0, len(s.nodes.Items))
			for _, node := range s.nodes.Items {
				instances = append(instances, node.Name)
			}
			fwdRuleFQN := gcp.ForwardingRuleFQN(s.project, s.region, fwdRule)
			consumers := toConsumerProjectLimits(s.spec.ConsumerAcceptList)

			notFound(m.GetFirewallPolicies(mctx, fw))
			noErr(m.CreateFirewallPolicies(mctx, fw, ports, gomock.InAnyOrder(instances)))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
			notFound(m.GetForwardingRule(mctx, fwdRule))
			noErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil, ports))
			notFound(m.GetServiceAttachment(mctx, svcAtt))
			callErr(m.CreateServiceAttachment(mctx, svcAtt, fwdRuleFQN, consumers, s.spec.NatSubnetFQNs), errors.New("can't create service attachment"))
		},
		expectedErrMsg: "can't create service attachment",
	}, {
		name: "Doesn't create or update the firewall if it already exists and is up to date",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			fwdRuleFQN := gcp.ForwardingRuleFQN(s.project, s.region, fwdRule)
			ports := map[int32]struct{}{}
			strPorts := make([]string, 0, len(ports))
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
				strPorts = append(strPorts, strconv.Itoa(int(port.NodePort)))
			}
			instances := make([]string, 0, len(s.nodes.Items))
			for _, node := range s.nodes.Items {
				instances = append(instances, node.Name)
			}
			consumers := toConsumerProjectLimits(s.spec.ConsumerAcceptList)
			fwRes := &computepb.FirewallPolicy{
				Rules: []*computepb.FirewallPolicyRule{{
					Match: &computepb.FirewallPolicyRuleMatcher{
						Layer4Configs: []*computepb.FirewallPolicyRuleMatcherLayer4Config{{
							IpProtocol: &tcp,
							Ports:      strPorts,
						}},
					},
				}},
			}
			once(m.GetFirewallPolicies(mctx, fw)).Return(fwRes, nil)

			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))

			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))

			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))

			notFound(m.GetForwardingRule(mctx, fwdRule))
			noErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil, ports))

			notFound(m.GetServiceAttachment(mctx, svcAtt))
			noErr(m.CreateServiceAttachment(mctx, svcAtt, fwdRuleFQN, consumers, s.spec.NatSubnetFQNs))
		},
	}, {
		name: "Updates the firewall",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			fwdRuleFQN := gcp.ForwardingRuleFQN(s.project, s.region, fwdRule)
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			instances := make([]string, 0, len(s.nodes.Items))
			for _, node := range s.nodes.Items {
				instances = append(instances, node.Name)
			}
			consumers := toConsumerProjectLimits(s.spec.ConsumerAcceptList)
			fwRes := &computepb.FirewallPolicy{
				Rules: []*computepb.FirewallPolicyRule{{
					Match: &computepb.FirewallPolicyRuleMatcher{
						Layer4Configs: []*computepb.FirewallPolicyRuleMatcherLayer4Config{{
							IpProtocol: &tcp,
							// No ports, so it needs to be updated.
						}},
					},
				}},
			}
			once(m.GetFirewallPolicies(mctx, fw)).Return(fwRes, nil)
			noErr(m.UpdateFirewallPolicies(mctx, fw, ports, gomock.InAnyOrder(instances)))

			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))

			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))

			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))

			notFound(m.GetForwardingRule(mctx, fwdRule))
			noErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil, ports))

			notFound(m.GetServiceAttachment(mctx, svcAtt))
			noErr(m.CreateServiceAttachment(mctx, svcAtt, fwdRuleFQN, consumers, s.spec.NatSubnetFQNs))
		},
		//TODO: Test endpoints detachment
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			initState := initialState()
			if tt.state != nil {
				initState = tt.state()
			}
			var c client.Client = fake.NewClientBuilder().
				WithLists(initState.nodes, initState.pods).
				WithObjects(initState.sts).
				Build()

			ctrl := gomock.NewController(t)

			gcpClient := mock.NewMockClient(ctrl)

			gcpClient.EXPECT().Project().AnyTimes().Return(initState.project)
			gcpClient.EXPECT().Region().AnyTimes().Return(initState.region)

			tt.setup(t, gcpClient, initState)

			r := New(c, gcpClient)
			req := reconcile.Request{
				NamespacedName: client.ObjectKey{
					Namespace: initState.sts.Namespace,
					Name:      initState.sts.Name,
				},
			}
			res, err := r.Reconcile(ctx, req)

			if tt.expectedErrMsg != "" {
				require.EqualError(t, err, tt.expectedErrMsg)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.expectedRes, res)
			if tt.assert != nil {
				tt.assert(t, c, initState)
			}
		})
	}
}

func notFound(c *gomock.Call) *gomock.Call {
	return getErr(c, gcp.ErrNotFound)
}

func noErr(c *gomock.Call) *gomock.Call {
	return once(c).Return(nil)
}

func getErr(c *gomock.Call, err error) *gomock.Call {
	return once(c).Return(nil, err)
}

func callErr(c *gomock.Call, err error) *gomock.Call {
	return once(c).Return(err)
}

func once(c *gomock.Call) *gomock.Call {
	return c.Times(1)
}

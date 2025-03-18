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
			consumers := toConsumerProjectLimits(s.spec.ConsumerAcceptList)

			notFound(m.GetFirewall(mctx, fw))
			noErr(m.CreateFirewall(mctx, fw, ports))

			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))

			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))

			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))

			notFound(m.GetForwardingRule(mctx, fwdRule))
			noErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil))

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
			getErr(mock.EXPECT().GetFirewall(mctx, fw), errors.New("can't get firewall"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't get firewall",
	}, {
		name: "Fails if it can't create the firewall",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			notFound(m.GetFirewall(mctx, fw))
			callErr(m.CreateFirewall(mctx, fw, ports), errors.New("can't create firewall"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't create firewall",
	}, {
		name: "Fails if it can't get the neg",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			notFound(m.GetFirewall(mctx, fw))
			noErr(m.CreateFirewall(mctx, fw, ports))
			getErr(m.GetNEG(mctx, neg), errors.New("can't get NEG"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't get NEG",
	}, {
		name: "Fails if it can't create the neg",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			notFound(m.GetFirewall(mctx, fw))
			noErr(m.CreateFirewall(mctx, fw, ports))
			notFound(m.GetNEG(mctx, neg))
			once(m.CreatePortmapNEG(mctx, neg)).Return(errors.New("can't create NEG"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't create NEG",
	}, {
		name: "Fails if it can't get the backend",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			notFound(m.GetFirewall(mctx, fw))
			noErr(m.CreateFirewall(mctx, fw, ports))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			getErr(m.GetBackendService(mctx, be), errors.New("can't get backend"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't get backend",
	}, {
		name: "Fails if it can't create the backend",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			notFound(m.GetFirewall(mctx, fw))
			noErr(m.CreateFirewall(mctx, fw, ports))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			callErr(m.CreateBackendService(mctx, be, neg), errors.New("can't create backend"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't create backend",
	}, {
		name: "Fails if it can't list the endpoints",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			notFound(m.GetFirewall(mctx, fw))
			noErr(m.CreateFirewall(mctx, fw, ports))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return(nil, errors.New("can't list endpoints"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't list endpoints",
	}, {
		name: "Fails if it can't attach the endpoints",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			notFound(m.GetFirewall(mctx, fw))
			noErr(m.CreateFirewall(mctx, fw, ports))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			callErr(m.AttachEndpoints(mctx, neg, s.portMappings()), errors.New("can't attach endpoints"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't attach endpoints",
	}, {
		name: "Fails if it can't get the forwarding rule",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			notFound(m.GetFirewall(mctx, fw))
			noErr(m.CreateFirewall(mctx, fw, ports))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
			getErr(m.GetForwardingRule(mctx, fwdRule), errors.New("can't get forwarding rule"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't get forwarding rule",
	}, {
		name: "Fails if it can't create the forwarding rule",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			notFound(m.GetFirewall(mctx, fw))
			noErr(m.CreateFirewall(mctx, fw, ports))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
			notFound(m.GetForwardingRule(mctx, fwdRule))
			callErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil), errors.New("can't create forwarding rule"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't create forwarding rule",
	}, {
		name: "Fails if it can't get the service attachment",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			notFound(m.GetFirewall(mctx, fw))
			noErr(m.CreateFirewall(mctx, fw, ports))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
			notFound(m.GetForwardingRule(mctx, fwdRule))
			noErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil))
			getErr(m.GetServiceAttachment(mctx, svcAtt), errors.New("can't get service attachment"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't get service attachment",
	}, {
		name: "Fails if it can't create the service attachment",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			ports := map[int32]struct{}{}
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
			}
			fwdRuleFQN := gcp.ForwardingRuleFQN(s.project, s.region, fwdRule)
			consumers := toConsumerProjectLimits(s.spec.ConsumerAcceptList)

			notFound(m.GetFirewall(mctx, fw))
			noErr(m.CreateFirewall(mctx, fw, ports))
			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
			notFound(m.GetForwardingRule(mctx, fwdRule))
			noErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil))
			notFound(m.GetServiceAttachment(mctx, svcAtt))
			callErr(m.CreateServiceAttachment(mctx, svcAtt, fwdRuleFQN, consumers, s.spec.NatSubnetFQNs), errors.New("can't create service attachment"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't create service attachment",
	}, {
		name: "Doesn't create or update the firewall if it already exists and is up to date",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			fwdRuleFQN := gcp.ForwardingRuleFQN(s.project, s.region, fwdRule)
			ports := map[int32]struct{}{}
			strPorts := make([]string, 0, len(s.spec.NodePorts))
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
				strPorts = append(strPorts, strconv.Itoa(int(port.NodePort)))
			}
			consumers := toConsumerProjectLimits(s.spec.ConsumerAcceptList)

			once(m.GetFirewall(mctx, fw)).Return(firewall(strPorts), nil)

			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
			notFound(m.GetForwardingRule(mctx, fwdRule))
			noErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil))
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
			consumers := toConsumerProjectLimits(s.spec.ConsumerAcceptList)

			once(m.GetFirewall(mctx, fw)).Return(firewall(nil), nil)
			noErr(m.UpdateFirewall(mctx, fw, ports))

			notFound(m.GetNEG(mctx, neg))
			noErr(m.CreatePortmapNEG(mctx, neg))
			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
			notFound(m.GetForwardingRule(mctx, fwdRule))
			noErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil))
			notFound(m.GetServiceAttachment(mctx, svcAtt))
			noErr(m.CreateServiceAttachment(mctx, svcAtt, fwdRuleFQN, consumers, s.spec.NatSubnetFQNs))
		},
	}, {
		name: "Doesn't create the NEG if it already exists",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			fwdRuleFQN := gcp.ForwardingRuleFQN(s.project, s.region, fwdRule)
			ports := map[int32]struct{}{}
			strPorts := make([]string, 0, len(s.spec.NodePorts))
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
				strPorts = append(strPorts, strconv.Itoa(int(port.NodePort)))
			}
			consumers := toConsumerProjectLimits(s.spec.ConsumerAcceptList)

			once(m.GetFirewall(mctx, fw)).Return(firewall(strPorts), nil)

			once(m.GetNEG(mctx, neg)).Return(&computepb.NetworkEndpointGroup{}, nil)

			notFound(m.GetBackendService(mctx, be))
			noErr(m.CreateBackendService(mctx, be, neg))
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
			notFound(m.GetForwardingRule(mctx, fwdRule))
			noErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil))
			notFound(m.GetServiceAttachment(mctx, svcAtt))
			noErr(m.CreateServiceAttachment(mctx, svcAtt, fwdRuleFQN, consumers, s.spec.NatSubnetFQNs))
		},
	}, {
		name: "Doesn't create the backend if it already exists",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			fwdRuleFQN := gcp.ForwardingRuleFQN(s.project, s.region, fwdRule)
			ports := map[int32]struct{}{}
			strPorts := make([]string, 0, len(s.spec.NodePorts))
			for _, port := range s.spec.NodePorts {
				ports[port.NodePort] = struct{}{}
				strPorts = append(strPorts, strconv.Itoa(int(port.NodePort)))
			}
			consumers := toConsumerProjectLimits(s.spec.ConsumerAcceptList)

			once(m.GetFirewall(mctx, fw)).Return(firewall(strPorts), nil)
			once(m.GetNEG(mctx, neg)).Return(&computepb.NetworkEndpointGroup{}, nil)

			once(m.GetBackendService(mctx, be)).Return(&computepb.BackendService{}, nil)

			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
			notFound(m.GetForwardingRule(mctx, fwdRule))
			noErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil))
			notFound(m.GetServiceAttachment(mctx, svcAtt))
			noErr(m.CreateServiceAttachment(mctx, svcAtt, fwdRuleFQN, consumers, s.spec.NatSubnetFQNs))
		},
	}, {
		name: "Doesn't create the forwarding rule if it already exists",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			fwdRuleFQN := gcp.ForwardingRuleFQN(s.project, s.region, fwdRule)
			strPorts := make([]string, 0, len(s.spec.NodePorts))
			for _, port := range s.spec.NodePorts {
				strPorts = append(strPorts, strconv.Itoa(int(port.NodePort)))
			}
			consumers := toConsumerProjectLimits(s.spec.ConsumerAcceptList)

			once(m.GetFirewall(mctx, fw)).Return(firewall(strPorts), nil)
			once(m.GetNEG(mctx, neg)).Return(&computepb.NetworkEndpointGroup{}, nil)
			once(m.GetBackendService(mctx, be)).Return(&computepb.BackendService{}, nil)
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))

			once(m.GetForwardingRule(mctx, fwdRule)).Return(&computepb.ForwardingRule{}, nil)

			notFound(m.GetServiceAttachment(mctx, svcAtt))
			noErr(m.CreateServiceAttachment(mctx, svcAtt, fwdRuleFQN, consumers, s.spec.NatSubnetFQNs))
		},
	}, {
		name: "Doesn't create the service attachment if it already exists",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			strPorts := make([]string, 0, len(s.spec.NodePorts))
			for _, port := range s.spec.NodePorts {
				strPorts = append(strPorts, strconv.Itoa(int(port.NodePort)))
			}

			once(m.GetFirewall(mctx, fw)).Return(firewall(strPorts), nil)
			once(m.GetNEG(mctx, neg)).Return(&computepb.NetworkEndpointGroup{}, nil)
			once(m.GetBackendService(mctx, be)).Return(&computepb.BackendService{}, nil)
			once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
			once(m.GetForwardingRule(mctx, fwdRule)).Return(&computepb.ForwardingRule{}, nil)

			once(m.GetServiceAttachment(mctx, svcAtt)).Return(&computepb.ServiceAttachment{}, nil)
		},
	}, {
		name: "Detaches obsolete endpoints",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			strPorts := make([]string, 0, len(s.spec.NodePorts))
			for _, port := range s.spec.NodePorts {
				strPorts = append(strPorts, strconv.Itoa(int(port.NodePort)))
			}
			currentMappings := []*gcp.PortMapping{{
				Port: 80, Instance: "instance1", InstancePort: 8080,
			}, {
				Port: 443, Instance: "instance2", InstancePort: 8443,
			}}

			once(m.GetFirewall(mctx, fw)).Return(firewall(strPorts), nil)
			once(m.GetNEG(mctx, neg)).Return(&computepb.NetworkEndpointGroup{}, nil)
			once(m.GetBackendService(mctx, be)).Return(&computepb.BackendService{}, nil)

			once(m.ListEndpoints(mctx, neg)).Return(currentMappings, nil)
			noErr(m.DetachEndpoints(mctx, neg, currentMappings))

			noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
			once(m.GetForwardingRule(mctx, fwdRule)).Return(&computepb.ForwardingRule{}, nil)
			once(m.GetServiceAttachment(mctx, svcAtt)).Return(&computepb.ServiceAttachment{}, nil)
		},
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

func TestDelete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := "prefix-"
	fw := firewallName(p)
	neg := negName(p)
	be := backendName(p)
	fwdRule := fwdRuleName(p)
	svcAtt := svcAttName(p)
	mctx := gomock.Any()

	expectCreation := func(m *mock.MockClientMockRecorder, s *state) {
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

		notFound(m.GetFirewall(mctx, fw))
		noErr(m.CreateFirewall(mctx, fw, ports))
		notFound(m.GetNEG(mctx, neg))
		noErr(m.CreatePortmapNEG(mctx, neg))
		notFound(m.GetBackendService(mctx, be))
		noErr(m.CreateBackendService(mctx, be, neg))
		once(m.ListEndpoints(mctx, neg)).Return([]*gcp.PortMapping{}, nil)
		noErr(m.AttachEndpoints(mctx, neg, s.portMappings()))
		notFound(m.GetForwardingRule(mctx, fwdRule))
		noErr(m.CreateForwardingRule(mctx, fwdRule, be, nil, nil))
		notFound(m.GetServiceAttachment(mctx, svcAtt))
		noErr(m.CreateServiceAttachment(mctx, svcAtt, fwdRuleFQN, consumers, s.spec.NatSubnetFQNs))
	}

	tests := []struct {
		name           string
		state          func() *state
		setup          func(t *testing.T, mock *mock.MockClient, s *state)
		assert         func(t *testing.T, c client.Client, s *state)
		expectedRes    reconcile.Result
		expectedErrMsg string
	}{{
		name: "Deletes everything",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			noErr(m.DeleteServiceAttachment(mctx, svcAtt))
			noErr(m.DeleteForwardingRule(mctx, fwdRule))
			noErr(m.DeleteBackendService(mctx, be))
			noErr(m.DeletePortmapNEG(mctx, neg))
			noErr(m.DeleteFirewall(mctx, fw))
		},
		assert: func(t *testing.T, c client.Client, s *state) {
			// Check that the nodeport was deleted too.
			nodeport := &corev1.Service{}
			err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: nodeportName("prefix-")}, nodeport)
			require.Error(t, err)
		},
		expectedRes: reconcile.Result{},
	}, {
		name: "Skips errors if the resources have been deleted",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			callErr(m.DeleteServiceAttachment(mctx, svcAtt), gcp.ErrNotFound)
			callErr(m.DeleteForwardingRule(mctx, fwdRule), gcp.ErrNotFound)
			callErr(m.DeleteBackendService(mctx, be), gcp.ErrNotFound)
			callErr(m.DeletePortmapNEG(mctx, neg), gcp.ErrNotFound)
			callErr(m.DeleteFirewall(mctx, fw), gcp.ErrNotFound)
		},
		expectedRes: reconcile.Result{},
	}, {
		name: "Returns an error if it can't delete the service attachment",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			callErr(m.DeleteServiceAttachment(mctx, svcAtt), errors.New("can't delete service attachment"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't delete service attachment",
	}, {
		name: "Returns an error if it can't delete the forwarding rule",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			noErr(m.DeleteServiceAttachment(mctx, svcAtt))
			callErr(m.DeleteForwardingRule(mctx, fwdRule), errors.New("can't delete forwarding rule"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't delete forwarding rule",
	}, {
		name: "Returns an error if it can't delete the backend service",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			noErr(m.DeleteServiceAttachment(mctx, svcAtt))
			noErr(m.DeleteForwardingRule(mctx, fwdRule))
			callErr(m.DeleteBackendService(mctx, be), errors.New("can't delete backend service"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't delete backend service",
	}, {
		name: "Returns an error if it can't delete the NEG",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			noErr(m.DeleteServiceAttachment(mctx, svcAtt))
			noErr(m.DeleteForwardingRule(mctx, fwdRule))
			noErr(m.DeleteBackendService(mctx, be))
			callErr(m.DeletePortmapNEG(mctx, neg), errors.New("can't delete NEG"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't delete NEG",
	}, {
		name: "Returns an error if it can't delete the firewall policies",
		setup: func(t *testing.T, mock *mock.MockClient, s *state) {
			m := mock.EXPECT()
			noErr(m.DeleteServiceAttachment(mctx, svcAtt))
			noErr(m.DeleteForwardingRule(mctx, fwdRule))
			noErr(m.DeleteBackendService(mctx, be))
			noErr(m.DeletePortmapNEG(mctx, neg))
			callErr(m.DeleteFirewall(mctx, fw), errors.New("can't delete firewall policies"))
		},
		expectedRes:    reconcile.Result{RequeueAfter: requeueDelay},
		expectedErrMsg: "can't delete firewall policies",
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
			expectCreation(gcpClient.EXPECT(), initState)

			r := New(c, gcpClient)
			req := reconcile.Request{
				NamespacedName: client.ObjectKey{
					Namespace: initState.sts.Namespace,
					Name:      initState.sts.Name,
				},
			}
			res, err := r.Reconcile(ctx, req)
			require.NoError(t, err)

			tt.setup(t, gcpClient, initState)

			// Delete the sts so that the reconcile loop will exercise the delete path.
			require.NoError(t, c.Delete(ctx, initState.sts))
			res, err = r.Reconcile(ctx, req)

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

func firewall(ports []string) *computepb.Firewall {
	return &computepb.Firewall{
		Allowed: []*computepb.Allowed{{
			IPProtocol: stringPtr("tcp"),
			Ports:      ports,
		}},
	}
}

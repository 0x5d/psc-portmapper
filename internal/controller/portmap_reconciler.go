package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/0x5d/psc-portmapper/internal/gcp"
	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	annotation         = "psc-portmapper.0x5d.org/spec"
	hostnameAnnotation = "kubernetes.io/hostname"

	managedByLabel = "app.kubernetes.io/managed-by"
	portmapperApp  = "psc-portmapper"

	finalizer = "psc-portmapper.0x5d.org/finalizer"
)

type PortmapReconciler struct {
	client.Client
	gcp gcp.Client
}

func New(c client.Client, gcpClient gcp.Client) *PortmapReconciler {
	return &PortmapReconciler{
		Client: c,
		gcp:    gcpClient,
	}
}

func (r *PortmapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.StatefulSet{}).
		WithEventFilter(isAnnotated()).
		Complete(r)
}

func (r *PortmapReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Reconciling PSC resources for STS.", "namespace", req.Namespace, "name", req.Name)

	sts := &appsv1.StatefulSet{}
	err := r.Get(ctx, req.NamespacedName, sts)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "Failed to get StatefulSet.")
			return reconcile.Result{}, err
		}
		log.Info("Couldn't find the STS that triggered the reconciliation.")
		return reconcile.Result{}, nil
	}

	a, ok := sts.Annotations[annotation]
	if !ok {
		log.Info("The STS is missing the " + annotation + " annotation")
		return reconcile.Result{}, nil
	}

	if controllerutil.AddFinalizer(sts, finalizer) {
		err := r.Update(ctx, sts)
		if err != nil {
			log.Error(err, "Failed to add finalizer to the STS.", "namespace", sts.Namespace, "name", sts.Name)
			return reconcile.Result{}, err
		}
	}

	var spec Spec
	err = json.Unmarshal([]byte(a), &spec)
	if err != nil {
		log.Error(err, "Couldn't decode the spec from the annotation.", "value", a)
		return reconcile.Result{RequeueAfter: 1 * time.Minute}, err
	}

	err = validateSpec(log, &spec)
	if err != nil {
		log.Error(err, "Invalid spec")
		return reconcile.Result{RequeueAfter: 1 * time.Minute}, err
	}

	if sts.DeletionTimestamp != nil {
		err := r.delete(ctx, log, &spec, sts)
		if err != nil {
			log.Error(err, "Failed to delete resources.")
			return reconcile.Result{RequeueAfter: 1 * time.Minute}, err
		}
		return reconcile.Result{}, nil
	}

	ports := map[int32]struct{}{}
	for _, p := range spec.NodePorts {
		ports[p.NodePort] = struct{}{}
	}
	nodePortName := types.NamespacedName{Name: nodeportName(spec.Prefix), Namespace: req.Namespace}
	err = r.reconcileNodePortService(ctx, log, nodePortName, spec.NodePorts, sts.Spec.Selector.MatchLabels)
	if err != nil {
		log.Error(err, "Failed to reconcile the NodePort service.")
		return reconcile.Result{}, err
	}

	pods := corev1.PodList{}
	err = r.List(ctx, &pods, client.MatchingLabels(sts.Spec.Selector.MatchLabels))
	if err != nil {
		log.Error(err, "Failed to list pods matching the STS' label.", "matchLabels", sts.Spec.Selector.MatchLabels)
		return reconcile.Result{RequeueAfter: 1 * time.Minute}, err
	}
	numPods := len(pods.Items)
	if numPods == 0 {
		log.Info("No pods matched the STS' labels. Are its replicas set to 0?")
	}

	nodes, err := r.getNodes(ctx, log, pods.Items)
	if err != nil {
		log.Error(err, "Failed to get the nodes the STS pods are scheduled on.")
		return reconcile.Result{RequeueAfter: 1 * time.Minute}, err
	}

	hostnames := make([]string, 0, numPods)
	for _, n := range nodes {
		hostnames = append(hostnames, n.ObjectMeta.Annotations[hostnameAnnotation])
	}

	mappings, err := r.getPortMappings(log, &spec, nodes, pods.Items)
	if err != nil {
		log.Error(err, "Failed to get the port mappings.")
		return reconcile.Result{RequeueAfter: 1 * time.Minute}, err
	}

	err = r.reconcile(ctx, log, &spec, ports, mappings, hostnames)
	if err != nil {
		log.Error(err, "Failed to reconcile the resources.")
		return reconcile.Result{RequeueAfter: 1 * time.Minute}, err
	}

	log.Info("Reconciliation successful.")
	return reconcile.Result{}, nil
}

func (r *PortmapReconciler) getPortMappings(log logr.Logger, spec *Spec, nodes map[string]*corev1.Node, pods []corev1.Pod) ([]*gcp.PortMapping, error) {
	numPods := len(pods)
	// Reconcile the resources.
	mappings := make([]*gcp.PortMapping, 0, numPods)
	for i := 0; i < numPods; i++ {
		for _, p := range spec.NodePorts {
			port := p.StartingPort + int32(i)
			nodeName := pods[i].Spec.NodeName
			node := nodes[nodeName]
			instance, err := fqInstaceName(node.Spec.ProviderID)
			if err != nil {
				log.Error(err, "Failed to get the fully qualified instance name for the node.", "node", nodeName)
				return nil, err
			}
			mappings = append(mappings, &gcp.PortMapping{
				Port:         port,
				Instance:     instance,
				InstancePort: p.NodePort,
			})
		}
	}
	return mappings, nil
}

func (r *PortmapReconciler) getNodes(ctx context.Context, log logr.Logger, pods []corev1.Pod) (map[string]*corev1.Node, error) {
	numPods := len(pods)
	nodesCh := make(chan *corev1.Node, numPods)
	wg := errgroup.Group{}
	for _, p := range pods {
		p := p
		nodeName := p.Spec.NodeName
		if nodeName == "" {
			log.Info("Skipping getting node info for unscheduled pod.", "namespace", p.Namespace, "name", p.Name)
			continue
		}
		wg.Go(func() error {
			node := &corev1.Node{}
			err := r.Get(ctx, types.NamespacedName{Name: nodeName}, node)
			if err != nil {
				return fmt.Errorf("failed to get node %s: %w", nodeName, err)
			}
			nodesCh <- node
			return nil
		})
	}
	err := wg.Wait()
	close(nodesCh)
	if err != nil {
		log.Error(err, "Failed to get the STS' pods' nodes.")
		return nil, err
	}
	nodes := make(map[string]*corev1.Node, numPods)

loop:
	for {
		select {
		case node, ok := <-nodesCh:
			if !ok {
				break loop
			}
			nodes[node.ObjectMeta.Name] = node
		default:
		}
	}

	return nodes, nil
}

func (r *PortmapReconciler) reconcile(ctx context.Context, log logr.Logger, spec *Spec, ports map[int32]struct{}, mappings []*gcp.PortMapping, hostnames []string) error {
	reconcilers := []struct {
		resource      string
		reconcileFunc func() error
	}{{
		"firewall",
		func() error {
			return r.reconcileFirewall(ctx, log, firewallName(spec.Prefix), ports, hostnames)
		},
	}, {
		"NEG",
		func() error {
			return r.reconcileNEG(ctx, log, negName(spec.Prefix))
		},
	}, {
		"backend",
		func() error {
			return r.reconcileBackend(ctx, log, backendName(spec.Prefix), negName(spec.Prefix))
		},
	}, {
		"endpoints",
		func() error {
			return r.reconcileEndpoints(ctx, log, negName(spec.Prefix), mappings)
		},
	}, {
		"forwarding rule",
		func() error {
			return r.reconcileForwardingRule(ctx, log, fwdRuleName(spec.Prefix), backendName(spec.Prefix), ports, spec.IP, spec.GlobalAccess)
		},
	}, {
		"service attachment",
		func() error {
			return r.reconcileServiceAttachment(ctx, log, svcAttName(spec.Prefix), fwdRuleName(spec.Prefix), spec.ConsumerAcceptList, spec.NatSubnetFQNs)
		},
	}}
	for _, r := range reconcilers {
		err := r.reconcileFunc()
		if err != nil {
			log.Error(err, "Failed to reconcile "+r.resource)
			return err
		}
	}
	return nil
}

func (r *PortmapReconciler) delete(ctx context.Context, log logr.Logger, spec *Spec, sts *appsv1.StatefulSet) error {
	np := types.NamespacedName{Name: nodeportName(spec.Prefix), Namespace: sts.Namespace}
	err := r.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: np.Name, Namespace: np.Namespace}})
	if err != nil {
		log.Error(err, "Failed to delete the NodePort service.", "namespace", np.Namespace, "name", np.Name)
	}
	deleters := []struct {
		resource   string
		deleteFunc func() error
	}{{
		"service attachment",
		func() error {
			return r.gcp.DeleteServiceAttachment(ctx, svcAttName(spec.Prefix))
		},
	}, {
		"forwarding rule",
		func() error {
			return r.gcp.DeleteForwardingRule(ctx, fwdRuleName(spec.Prefix))
		},
	}, {
		"backend",
		func() error {
			return r.gcp.DeleteBackendService(ctx, backendName(spec.Prefix))
		},
	}, {
		"NEG",
		func() error {
			return r.gcp.DeletePortmapNEG(ctx, negName(spec.Prefix))
		},
	}, {
		"firewall",
		func() error {
			return r.gcp.DeleteFirewallPolicies(ctx, firewallName(spec.Prefix))
		},
	}}
	for _, d := range deleters {
		err = d.deleteFunc()
		if err == nil {
			log.Info("Resource deleted.", "type", d.resource)
			continue
		}
		if !errors.Is(err, gcp.ErrNotFound) {
			log.Error(err, "Failed to delete resource.", "type", d.resource)
			return err
		}
		log.Info("Resource not found, so nothing to delete. Was it removed manually or by another process?", "type", d.resource)
	}

	if controllerutil.RemoveFinalizer(sts, finalizer) {
		err := r.Update(ctx, sts)
		if err != nil {
			log.Error(err, "Failed to remove finalizer from the STS.", "namespace", sts.Namespace, "name", sts.Name)
			return err
		}
	}
	return nil
}

func (r *PortmapReconciler) reconcileNodePortService(
	ctx context.Context,
	log logr.Logger,
	name types.NamespacedName,
	ports map[string]PortConfig,
	selector map[string]string,
) error {
	svcPorts := make([]corev1.ServicePort, 0, len(ports))
	for portName, m := range ports {
		svcPorts = append(svcPorts, corev1.ServicePort{
			Name:     portName,
			Protocol: corev1.ProtocolTCP,
			Port:     m.NodePort,
			TargetPort: intstr.IntOrString{
				Type:   intstr.Int,
				IntVal: m.ContainerPort,
			},
		})
	}
	nodePort := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
			Labels:    map[string]string{managedByLabel: portmapperApp},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: selector,
			Ports:    svcPorts,
		},
	}
	var np corev1.Service
	err := r.Get(ctx, name, &np)
	if err == nil {
		err := r.Update(ctx, &nodePort)
		if err != nil {
			log.Error(err, "Failed to update the NodePort service.")
			return err
		}
		return nil
	}
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "Failed to get the NodePort service.")
		return err
	}

	err = r.Create(ctx, &nodePort)
	if err != nil {
		log.Error(err, "Failed to create the NodePort service.")
		return err
	}
	return nil
}

func (r *PortmapReconciler) reconcileFirewall(ctx context.Context, log logr.Logger, name string, ports map[int32]struct{}, hostnames []string) error {
	fw, err := r.gcp.GetFirewallPolicies(ctx, name)
	if err == nil && gcp.FirewallNeedsUpdate(fw, ports) {
		err = r.gcp.UpdateFirewallPolicies(ctx, name, ports, hostnames)
		if err != nil {
			log.Error(err, "Failed to update firewall policy.", "name", name, "ports", ports, "instances", hostnames)
			return err
		}
	}
	if !errors.Is(err, gcp.ErrNotFound) {
		log.Error(err, "Got an unexpected error trying to get firewall policy.", "name", name)
		return err
	}
	err = r.gcp.CreateFirewallPolicies(ctx, name, ports, hostnames)
	if err != nil {
		log.Error(err, "Failed to create firewall policy.", "ports", ports, "instances", hostnames)
		return err
	}
	return nil
}

func (r *PortmapReconciler) reconcileNEG(ctx context.Context, log logr.Logger, name string) error {
	_, err := r.gcp.GetNEG(ctx, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, gcp.ErrNotFound) {
		log.Error(err, "Got an unexpected error trying to get the NEG.", "name", name)
		return err
	}
	err = r.gcp.CreatePortmapNEG(ctx, name)
	if err != nil {
		log.Error(err, "Failed to create the NEG.")
		return err
	}
	return nil
}

func (r *PortmapReconciler) reconcileBackend(ctx context.Context, log logr.Logger, name, neg string) error {
	_, err := r.gcp.GetBackendService(ctx, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, gcp.ErrNotFound) {
		log.Error(err, "Got an unexpected error trying to get the backend.", "name", name)
		return err
	}
	err = r.gcp.CreateBackendService(ctx, name, neg)
	if err != nil {
		log.Error(err, "Failed to create the backend.")
		return err
	}
	return nil
}

func (r *PortmapReconciler) reconcileEndpoints(ctx context.Context, log logr.Logger, neg string, mappings []*gcp.PortMapping) error {
	eps, err := r.gcp.ListEndpoints(ctx, neg)
	if err != nil {
		if errors.Is(err, gcp.ErrNotFound) {
			log.Error(err, "Couldn't attach the endpoints to the NEG. Was the NEG removed manually or by another process?", "name", neg)
		} else {
			log.Error(err, "Got an unexpected error trying to list the NEG's endpoints.", "name", neg)
		}
		return err
	}
	// Endpoints must be detached first because the API doesn't allow attaching registering
	// endpoints with the same port twice.
	obsolete := getObsoletePortMappings(mappings, eps)
	if len(obsolete) > 0 {
		err = r.gcp.DetachEndpoints(ctx, neg, obsolete)
		if err != nil {
			log.Error(err, "Failed to detach obsolete endpoints from the NEG.", "name", neg)
			return err
		}
	}

	err = r.gcp.AttachEndpoints(ctx, neg, mappings)
	if err != nil {
		log.Error(err, "Failed to attach the endpoints to the NEG.", "name", neg)
		return err
	}
	return nil
}

func (r *PortmapReconciler) reconcileForwardingRule(
	ctx context.Context,
	log logr.Logger,
	name string,
	backend string,
	ports map[int32]struct{},
	ip *string,
	globalAccess *bool,
) error {
	_, err := r.gcp.GetForwardingRule(ctx, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, gcp.ErrNotFound) {
		log.Error(err, "Got an unexpected error trying to get the backend.", "name", name)
		return err
	}
	err = r.gcp.CreateForwardingRule(ctx, name, backend, ip, globalAccess, ports)
	if err != nil {
		log.Error(err, "Failed to create the forwarding rule.")
		return err
	}
	return nil
}

func (r *PortmapReconciler) reconcileServiceAttachment(ctx context.Context, log logr.Logger, name string, fwdRule string, consumers []*Consumer, natSubnetFQNs []string) error {
	_, err := r.gcp.GetServiceAttachment(ctx, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, gcp.ErrNotFound) {
		log.Error(err, "Got an unexpected error trying to get the service attachment.", "name", name)
		return err
	}
	fwdRuleFQN := gcp.ForwardingRuleFQN(r.gcp.Project(), r.gcp.Region(), fwdRule)
	err = r.gcp.CreateServiceAttachment(ctx, name, fwdRuleFQN, toConsumerProjectLimits(consumers), natSubnetFQNs)
	if err != nil {
		log.Error(err, "Failed to create the service attachment.")
		return err
	}
	return nil
}

func nodeportName(prefix string) string {
	return nameBase(prefix)
}

func firewallName(prefix string) string {
	return nameBase(prefix) + "-firewall"
}

func negName(prefix string) string {
	return nameBase(prefix) + "-neg"
}

func backendName(prefix string) string {
	return nameBase(prefix) + "-backend"
}

func fwdRuleName(prefix string) string {
	return nameBase(prefix) + "-fwdrule"
}

func svcAttName(prefix string) string {
	return nameBase(prefix) + "-svcatt"
}

func nameBase(prefix string) string {
	return prefix + portmapperApp
}

// returns the *gcp.PortMapping that are in the second slice but not in the first
func getObsoletePortMappings(expected, actual []*gcp.PortMapping) []*gcp.PortMapping {
	// Create a map to store the port mappings from the first slice
	portMap := make(map[gcp.PortMapping]struct{})

	// Add each port mapping from the first slice to the map
	for _, pm := range expected {
		portMap[*pm] = struct{}{}
	}

	// Iterate over the second slice and collect port mappings not in the first slice
	var diff []*gcp.PortMapping
	for _, pm := range actual {
		if _, ok := portMap[*pm]; !ok {
			diff = append(diff, pm)
		}
	}

	return diff
}

func toConsumerProjectLimits(cs []*Consumer) []*computepb.ServiceAttachmentConsumerProjectLimit {
	consumerAcceptList := make([]*computepb.ServiceAttachmentConsumerProjectLimit, 0, len(cs))
	for _, c := range cs {
		consumerAcceptList = append(consumerAcceptList, &computepb.ServiceAttachmentConsumerProjectLimit{
			ProjectIdOrNum:  c.ProjectIdOrNum,
			NetworkUrl:      c.NetworkFQN,
			ConnectionLimit: &c.ConnectionLimit,
		})
	}
	return consumerAcceptList
}

var providerIDRegexp = regexp.MustCompile(`^gce://([^/]+)/([^/]+)/([^/]+)$`)

func fqInstaceName(nodeProviderID string) (string, error) {
	// gce://<project-id>/<zone>/<instance-name>
	// into
	// projects/<project-id>/zones/<zone>/instances/<instance-name>
	matches := providerIDRegexp.FindStringSubmatch(nodeProviderID)
	if len(matches) != 4 {
		return "", fmt.Errorf("invalid provider ID format, expected 'gce://<project-id>/<zone>/<instance-name>', got: %s", nodeProviderID)
	}

	// matches[0] is the full string, matches[1:] are the capture groups
	projectID := matches[1]
	zone := matches[2]
	instanceName := matches[3]

	return fmt.Sprintf("projects/%s/zones/%s/instances/%s", projectID, zone, instanceName), nil
}

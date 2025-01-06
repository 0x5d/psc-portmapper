package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/0x5d/psc-portmapper/internal/gcp"
	"github.com/go-logr/logr"
	"github.com/googleapis/gax-go/v2/apierror"
	"golang.org/x/sync/errgroup"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	annotation         = "0x5d.org/psc-portmapper"
	hostnameAnnotation = "kubernetes.io/hostname"
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
			log.Error(err, "Failed to get Portmap")
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

	var spec Spec
	err = json.Unmarshal([]byte(a), &spec)
	if err != nil {
		log.Error(err, "Couldn't decode the spec from the annotation.", "value", a)
		return reconcile.Result{RequeueAfter: 1 * time.Minute}, err
	}

	if sts.DeletionTimestamp != nil {
		// Deal with deletion, set finalizer, etc.
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
	nodesCh := make(chan *corev1.Node, numPods)
	wg := errgroup.Group{}
	// TODO: MATCH THE NODE TO THE ORDINAL
	for _, p := range pods.Items {
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
	err = wg.Wait()
	close(nodesCh)
	if err != nil {
		log.Error(err, "Failed to get the STS' pods' nodes.")
	}
	nodes := map[string]*corev1.Node{}
	hostnames := make([]string, 0, numPods)
loop:
	for {
		select {
		case node := <-nodesCh:
			nodes[node.ObjectMeta.Name] = node
			hostnames = append(hostnames, node.ObjectMeta.Annotations[hostnameAnnotation])
		default:
			break loop
		}
	}

	// Reconcile the resources.
	ports := make([]int32, 0, numPods)
	for i := 0; i < numPods; i++ {
		ports = append(ports, spec.StartPort+int32(i))
	}

	fw := firewallName(spec.Prefix)
	err = r.reconcileFirewall(ctx, log, fw, ports, hostnames)
	if err != nil {
		log.Error(err, "Unable to reconcile firewall", "name", fw)
		return reconcile.Result{}, err
	}

	neg := negName(spec.Prefix)
	err = r.reconcileNEG(ctx, log, neg)
	if err != nil {
		log.Error(err, "Unable to reconcile NEG", "name", neg)
		return reconcile.Result{}, err
	}

	backend := backendName(spec.Prefix)
	err = r.reconcileBackend(ctx, log, backend)
	if err != nil {
		log.Error(err, "Unable to reconcile backend", "name", neg)
		return reconcile.Result{}, err
	}

	// err = r.reconcileEndpoints(ctx, log)
	// if err != nil {
	// 	log.Error(err, "Unable to reconcile backend", "name", neg)
	// 	return reconcile.Result{}, err
	// }

	return reconcile.Result{}, nil
}

func (r *PortmapReconciler) reconcileFirewall(ctx context.Context, log logr.Logger, name string, ports []int32, hostnames []string) error {
	fw, err := r.gcp.GetFirewallPolicies(ctx, name)
	if err == nil && gcp.FirewallNeedsUpdate(fw, ports) {
		err = r.gcp.UpdateFirewallPolicies(ctx, name, ports, hostnames)
		if err != nil {
			log.Error(err, "Failed to update firewall policy.", "name", name, "ports", ports, "instances", hostnames)
			return err
		}
	}
	var ae *apierror.APIError
	if !errors.As(err, &ae) || ae.HTTPCode() != http.StatusNotFound {
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
	var ae *apierror.APIError
	if !errors.As(err, &ae) || ae.HTTPCode() != http.StatusNotFound {
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

func (r *PortmapReconciler) reconcileBackend(ctx context.Context, log logr.Logger, name string) error {
	_, err := r.gcp.GetBackendService(ctx, name)
	if err == nil {
		return nil
	}
	var ae *apierror.APIError
	if !errors.As(err, &ae) || ae.HTTPCode() != http.StatusNotFound {
		log.Error(err, "Got an unexpected error trying to get the backend.", "name", name)
		return err
	}
	err = r.gcp.CreatePortmapNEG(ctx, name)
	if err != nil {
		log.Error(err, "Failed to create the backend.")
		return err
	}
	return nil
}

func (r *PortmapReconciler) reconcileEndpoints(ctx context.Context, log logr.Logger, neg string, mappings []*gcp.PortMapping) error {
	eps, err := r.gcp.ListEndpoints(ctx, neg)
	if err != nil {
		var ae *apierror.APIError
		if errors.As(err, &ae) && ae.HTTPCode() == http.StatusNotFound {
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

func firewallName(prefix string) string {
	return prefix + "psc-portmapper-firewall"
}

func negName(prefix string) string {
	return prefix + "psc-portmapper-neg"
}

func backendName(prefix string) string {
	return prefix + "psc-portmapper-backend"
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

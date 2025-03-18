//go:generate go run go.uber.org/mock/mockgen -destination mock/client.go -package mock . Client
package gcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/google/uuid"
	"github.com/googleapis/gax-go/v2"
	"github.com/googleapis/gax-go/v2/apierror"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type ClientError struct {
	msg    string
	status int
}

var ErrNotFound = &ClientError{msg: "not found", status: http.StatusNotFound}

func (e *ClientError) Error() string {
	return fmt.Sprintf("%s (status %d)", e.msg, e.status)
}

type Client interface {
	// Accessors
	Project() string
	Region() string
	// NEGs API
	GetNEG(ctx context.Context, name string) (*computepb.NetworkEndpointGroup, error)
	CreatePortmapNEG(ctx context.Context, name string) error
	DeletePortmapNEG(ctx context.Context, name string) error
	ListEndpoints(ctx context.Context, neg string) ([]*PortMapping, error)
	AttachEndpoints(ctx context.Context, neg string, mappings []*PortMapping) error
	DetachEndpoints(ctx context.Context, neg string, mappings []*PortMapping) error
	// Firewalls API
	GetFirewall(ctx context.Context, name string) (*computepb.Firewall, error)
	CreateFirewall(ctx context.Context, name string, ports map[int32]struct{}) error
	UpdateFirewall(ctx context.Context, name string, ports map[int32]struct{}) error
	DeleteFirewall(ctx context.Context, name string) error
	// Backend Services API
	GetBackendService(ctx context.Context, name string) (*computepb.BackendService, error)
	CreateBackendService(ctx context.Context, name string, neg string) error
	DeleteBackendService(ctx context.Context, name string) error
	// Forwarding Rules API
	GetForwardingRule(ctx context.Context, name string) (*computepb.ForwardingRule, error)
	CreateForwardingRule(ctx context.Context, name, backendSvc string, ip *string, globalAccess *bool) error
	DeleteForwardingRule(ctx context.Context, name string) error
	// Service Attachments API
	GetServiceAttachment(ctx context.Context, name string) (*computepb.ServiceAttachment, error)
	CreateServiceAttachment(ctx context.Context, name, fwdRuleFQN string, consumers []*computepb.ServiceAttachmentConsumerProjectLimit, natSubnetFQNs []string) error
	DeleteServiceAttachment(ctx context.Context, name string) error
}

type GCPClient struct {
	cfg         *ClientConfig
	negs        *compute.RegionNetworkEndpointGroupsClient
	firewalls   *compute.FirewallsClient
	backendSvcs *compute.RegionBackendServicesClient
	fwdRules    *compute.ForwardingRulesClient
	svcAtts     *compute.ServiceAttachmentsClient
}

type PortMapping struct {
	Port         int32
	Instance     string
	InstancePort int32
}

var _ Client = &GCPClient{}

func NewClient(ctx context.Context, cfg ClientConfig, opts ...option.ClientOption) (*GCPClient, error) {
	if !isFQN(cfg.Network) {
		cfg.Network = NetworkFQN(cfg.Project, cfg.Network)
	}
	if !isFQN(cfg.Subnetwork) {
		cfg.Subnetwork = SubnetFQN(cfg.Project, cfg.Region, cfg.Subnetwork)
	}
	negs, err := compute.NewRegionNetworkEndpointGroupsRESTClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	firewalls, err := compute.NewFirewallsRESTClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	backendSvcs, err := compute.NewRegionBackendServicesRESTClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	fwdRules, err := compute.NewForwardingRulesRESTClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	svcAtts, err := compute.NewServiceAttachmentsRESTClient(ctx, opts...)
	return &GCPClient{cfg: &cfg, negs: negs, firewalls: firewalls, backendSvcs: backendSvcs, fwdRules: fwdRules, svcAtts: svcAtts}, nil
}

func (c *GCPClient) Project() string {
	return c.cfg.Project
}

func (c *GCPClient) Region() string {
	return c.cfg.Region
}

func (c *GCPClient) GetNEG(ctx context.Context, name string) (*computepb.NetworkEndpointGroup, error) {
	req := &computepb.GetRegionNetworkEndpointGroupRequest{
		Project:              c.cfg.Project,
		Region:               c.cfg.Region,
		NetworkEndpointGroup: name,
	}
	return get(ctx, c.negs.Get, req)
}

func (c *GCPClient) CreatePortmapNEG(ctx context.Context, name string) error {
	reqID := uuid.New().String()
	endpointType := computepb.NetworkEndpointGroup_GCE_VM_IP_PORTMAP.String()
	req := &computepb.InsertRegionNetworkEndpointGroupRequest{
		RequestId: &reqID,
		Project:   c.cfg.Project,
		Region:    c.cfg.Region,
		NetworkEndpointGroupResource: &computepb.NetworkEndpointGroup{
			Name:                &name,
			Network:             &c.cfg.Network,
			Subnetwork:          &c.cfg.Subnetwork,
			Annotations:         c.cfg.Annotations,
			NetworkEndpointType: &endpointType,
		},
	}
	return call(ctx, c.negs.Insert, req)
}

func (c *GCPClient) DeletePortmapNEG(
	ctx context.Context,
	name string,
) error {
	reqID := uuid.New().String()
	req := &computepb.DeleteRegionNetworkEndpointGroupRequest{
		RequestId:            &reqID,
		Project:              c.cfg.Project,
		Region:               c.cfg.Region,
		NetworkEndpointGroup: name,
	}
	return call(ctx, c.negs.Delete, req)
}

func (c *GCPClient) ListEndpoints(ctx context.Context, neg string) ([]*PortMapping, error) {
	req := &computepb.ListNetworkEndpointsRegionNetworkEndpointGroupsRequest{
		Project:              c.cfg.Project,
		Region:               c.cfg.Region,
		NetworkEndpointGroup: neg,
	}
	it := c.negs.ListNetworkEndpoints(ctx, req, callOpts()...)
	ms := []*PortMapping{}
	for {
		resp, err := it.Next()
		if err != nil {
			if err == iterator.Done {
				return ms, nil
			}
			return nil, err
		}
		ms = append(ms, &PortMapping{
			Port:         *resp.NetworkEndpoint.ClientDestinationPort,
			Instance:     *resp.NetworkEndpoint.Instance,
			InstancePort: *resp.NetworkEndpoint.Port,
		})
	}
}

func (c *GCPClient) AttachEndpoints(ctx context.Context, neg string, mappings []*PortMapping) error {
	ms := make([]*computepb.NetworkEndpoint, 0, len(mappings))
	for _, m := range mappings {
		ms = append(ms, &computepb.NetworkEndpoint{
			Annotations:           c.cfg.Annotations,
			ClientDestinationPort: &m.Port,
			Instance:              &m.Instance,
			Port:                  &m.InstancePort,
		})
	}
	reqID := uuid.New().String()
	req := &computepb.AttachNetworkEndpointsRegionNetworkEndpointGroupRequest{
		RequestId:            &reqID,
		Project:              c.cfg.Project,
		Region:               c.cfg.Region,
		NetworkEndpointGroup: neg,
		RegionNetworkEndpointGroupsAttachEndpointsRequestResource: &computepb.RegionNetworkEndpointGroupsAttachEndpointsRequest{
			NetworkEndpoints: ms,
		},
	}
	return call(ctx, c.negs.AttachNetworkEndpoints, req)
}

func (c *GCPClient) DetachEndpoints(ctx context.Context, neg string, mappings []*PortMapping) error {
	ms := make([]*computepb.NetworkEndpoint, 0, len(mappings))
	for _, m := range mappings {
		ms = append(ms, &computepb.NetworkEndpoint{
			Annotations:           c.cfg.Annotations,
			ClientDestinationPort: &m.Port,
			Instance:              &m.Instance,
			Port:                  &m.InstancePort,
		})
	}
	reqID := uuid.New().String()
	req := &computepb.DetachNetworkEndpointsRegionNetworkEndpointGroupRequest{
		RequestId:            &reqID,
		Project:              c.cfg.Project,
		Region:               c.cfg.Region,
		NetworkEndpointGroup: neg,
		RegionNetworkEndpointGroupsDetachEndpointsRequestResource: &computepb.RegionNetworkEndpointGroupsDetachEndpointsRequest{
			NetworkEndpoints: ms,
		},
	}
	return call(ctx, c.negs.DetachNetworkEndpoints, req)
}

func (c *GCPClient) GetFirewall(ctx context.Context, name string) (*computepb.Firewall, error) {
	req := &computepb.GetFirewallRequest{Project: c.cfg.Project, Firewall: name}
	return get(ctx, c.firewalls.Get, req)
}

func (c *GCPClient) CreateFirewall(ctx context.Context, name string, ports map[int32]struct{}) error {
	reqID := uuid.New().String()
	tcp := "tcp"
	priority := int32(1000)
	ingress := computepb.FirewallPolicyRule_INGRESS.String()
	strPorts := toSortedStr(ports)

	req := &computepb.InsertFirewallRequest{
		RequestId: &reqID,
		Project:   c.cfg.Project,
		FirewallResource: &computepb.Firewall{
			Name:      &name,
			Direction: &ingress,
			Network:   &c.cfg.Network,
			Priority:  &priority,
			//TODO: TargetTags: []string{}, OR DestinationRanges: []string{},
			Allowed: []*computepb.Allowed{{
				IPProtocol: &tcp,
				Ports:      strPorts,
			}},
		},
	}
	return call(ctx, c.firewalls.Insert, req)
}

func (c *GCPClient) UpdateFirewall(ctx context.Context, name string, ports map[int32]struct{}) error {
	reqID := uuid.New().String()
	tcp := "tcp"
	strPorts := toSortedStr(ports)
	req := &computepb.PatchFirewallRequest{
		RequestId: &reqID,
		Project:   c.cfg.Project,
		Firewall:  name,
		FirewallResource: &computepb.Firewall{
			Name: &name,
			Allowed: []*computepb.Allowed{{
				IPProtocol: &tcp,
				Ports:      strPorts,
			}},
		},
	}
	return call(ctx, c.firewalls.Patch, req)
}

func (c *GCPClient) DeleteFirewall(
	ctx context.Context,
	name string,
) error {
	reqID := uuid.New().String()
	req := &computepb.DeleteFirewallRequest{
		RequestId: &reqID,
		Project:   c.cfg.Project,
		Firewall:  name,
	}
	return call(ctx, c.firewalls.Delete, req)
}

func (c *GCPClient) GetBackendService(ctx context.Context, name string) (*computepb.BackendService, error) {
	req := &computepb.GetRegionBackendServiceRequest{
		Project:        c.cfg.Project,
		Region:         c.cfg.Region,
		BackendService: name,
	}
	return get(ctx, c.backendSvcs.Get, req)
}

func (c *GCPClient) CreateBackendService(ctx context.Context, name string, neg string) error {
	reqID := uuid.New().String()
	protocol := computepb.BackendService_TCP.String()
	negFQN := NEGFQN(c.cfg.Project, c.cfg.Region, neg)
	internal := computepb.BackendService_INTERNAL.String()
	req := &computepb.InsertRegionBackendServiceRequest{
		RequestId: &reqID,
		Project:   c.cfg.Project,
		Region:    c.cfg.Region,
		BackendServiceResource: &computepb.BackendService{
			Name:                &name,
			Network:             &c.cfg.Network,
			Protocol:            &protocol,
			LoadBalancingScheme: &internal,
			Backends: []*computepb.Backend{{
				Group: &negFQN,
				// TODO:
				// MaxConnections, etc.
			}},
		},
	}
	return call(ctx, c.backendSvcs.Insert, req)
}

func (c *GCPClient) DeleteBackendService(
	ctx context.Context,
	name string,
) error {
	reqID := uuid.New().String()
	req := &computepb.DeleteRegionBackendServiceRequest{
		RequestId:      &reqID,
		Project:        c.cfg.Project,
		Region:         c.cfg.Region,
		BackendService: name,
	}
	return call(ctx, c.backendSvcs.Delete, req)
}

func (c *GCPClient) GetForwardingRule(ctx context.Context, name string) (*computepb.ForwardingRule, error) {
	req := &computepb.GetForwardingRuleRequest{
		Project:        c.cfg.Project,
		Region:         c.cfg.Region,
		ForwardingRule: name,
	}
	return get(ctx, c.fwdRules.Get, req)
}

func (c *GCPClient) CreateForwardingRule(ctx context.Context, name, backendSvc string, ip *string, globalAccess *bool) error {
	reqID := uuid.New().String()
	scheme := computepb.BackendService_INTERNAL.String()
	tcp := computepb.ForwardingRule_TCP.String()
	backendFQN := BackendServiceFQN(c.cfg.Project, c.cfg.Region, backendSvc)
	// AllPorts must be set to true when the target is a backend service with a port mapping network endpoint group backend.
	allPorts := true
	req := &computepb.InsertForwardingRuleRequest{
		RequestId: &reqID,
		Project:   c.cfg.Project,
		Region:    c.cfg.Region,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name:                &name,
			IPAddress:           ip,
			IPProtocol:          &tcp,
			AllowGlobalAccess:   globalAccess,
			BackendService:      &backendFQN,
			Network:             &c.cfg.Network,
			Subnetwork:          &c.cfg.Subnetwork,
			AllPorts:            &allPorts,
			LoadBalancingScheme: &scheme,
		},
	}
	return call(ctx, c.fwdRules.Insert, req)
}

func (c *GCPClient) DeleteForwardingRule(
	ctx context.Context,
	name string,
) error {
	reqID := uuid.New().String()
	req := &computepb.DeleteForwardingRuleRequest{
		RequestId:      &reqID,
		Project:        c.cfg.Project,
		Region:         c.cfg.Region,
		ForwardingRule: name,
	}
	return call(ctx, c.fwdRules.Delete, req)
}

func (c *GCPClient) GetServiceAttachment(ctx context.Context, name string) (*computepb.ServiceAttachment, error) {
	req := &computepb.GetServiceAttachmentRequest{
		Project:           c.cfg.Project,
		Region:            c.cfg.Region,
		ServiceAttachment: name,
	}
	return get(ctx, c.svcAtts.Get, req)
}

func (c *GCPClient) CreateServiceAttachment(
	ctx context.Context,
	name,
	fwdRuleFQN string,
	consumers []*computepb.ServiceAttachmentConsumerProjectLimit,
	natSubnetFQNs []string,
) error {
	reqID := uuid.New().String()
	acceptAuto := computepb.ServiceAttachment_ACCEPT_AUTOMATIC.String()
	req := &computepb.InsertServiceAttachmentRequest{
		RequestId: &reqID,
		Project:   c.cfg.Project,
		Region:    c.cfg.Region,
		ServiceAttachmentResource: &computepb.ServiceAttachment{
			Name:                   &name,
			ProducerForwardingRule: &fwdRuleFQN,
			ConsumerAcceptLists:    consumers,
			NatSubnets:             natSubnetFQNs,
			ConnectionPreference:   &acceptAuto,
		},
	}
	return call(ctx, c.svcAtts.Insert, req)
}

func (c *GCPClient) DeleteServiceAttachment(
	ctx context.Context,
	name string,
) error {
	reqID := uuid.New().String()
	req := &computepb.DeleteServiceAttachmentRequest{
		RequestId:         &reqID,
		Project:           c.cfg.Project,
		Region:            c.cfg.Region,
		ServiceAttachment: name,
	}
	return call(ctx, c.svcAtts.Delete, req)
}

func callOpts() []gax.CallOption {
	return []gax.CallOption{
		gax.WithRetry(func() gax.Retryer {
			return gax.OnCodes(nil, gax.Backoff{})
		}),
	}
}

func get[T any, U any, F func(context.Context, T, ...gax.CallOption) (U, error)](ctx context.Context, f F, req T) (U, error) {
	u, err := f(ctx, req, callOpts()...)
	if err == nil {
		return u, nil
	}
	return u, toClientError(err)
}

func call[T any, F func(context.Context, T, ...gax.CallOption) (*compute.Operation, error)](ctx context.Context, f F, req T) error {
	op, err := f(ctx, req)
	if err != nil {
		return toClientError(err)
	}
	err = op.Wait(ctx, callOpts()...)
	if err == nil {
		return nil
	}
	return toClientError(err)
}

func toClientError(err error) error {
	var ae *apierror.APIError
	if errors.As(err, &ae) {
		if ae.HTTPCode() == http.StatusNotFound {
			return ErrNotFound
		}
		msg := fmt.Sprintf("%s: %s", ae.Error(), ae.Details())
		return &ClientError{msg: msg, status: ae.HTTPCode()}
	}
	return &ClientError{msg: err.Error(), status: -1}
}

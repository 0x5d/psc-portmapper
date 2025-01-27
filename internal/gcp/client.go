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
	// NEGs API
	GetNEG(ctx context.Context, name string) (*computepb.NetworkEndpointGroup, error)
	CreatePortmapNEG(ctx context.Context, name string) error
	ListEndpoints(ctx context.Context, neg string) ([]*PortMapping, error)
	AttachEndpoints(ctx context.Context, neg string, mappings []*PortMapping) error
	DetachEndpoints(ctx context.Context, neg string, mappings []*PortMapping) error
	// Firewalls API
	GetFirewallPolicies(ctx context.Context, name string) (*computepb.FirewallPolicy, error)
	CreateFirewallPolicies(ctx context.Context, name string, ports []int32, instances []string) error
	UpdateFirewallPolicies(ctx context.Context, name string, ports []int32, instances []string) error
	// Backend Services API
	GetBackendService(ctx context.Context, name string) (*computepb.BackendService, error)
	CreateBackendService(ctx context.Context, name string, neg string) error
	// Forwarding Rules API
	GetForwardingRule(ctx context.Context, name string) (*computepb.ForwardingRule, error)
	CreateForwardingRule(ctx context.Context, name, backendSvc string, ip *string, globalAccess *bool, ports []int32) error
	// Service Attachments API
	GetServiceAttachment(ctx context.Context, name string) (*computepb.ServiceAttachment, error)
	CreateServiceAttachment(ctx context.Context, name, fwdRuleFQN string, consumers []*computepb.ServiceAttachmentConsumerProjectLimit, natSubnetFQNs []string) error
}

type GCPClient struct {
	cfg         *ClientConfig
	negs        *compute.RegionNetworkEndpointGroupsClient
	firewalls   *compute.RegionNetworkFirewallPoliciesClient
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
	negs, err := compute.NewRegionNetworkEndpointGroupsRESTClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	firewalls, err := compute.NewRegionNetworkFirewallPoliciesRESTClient(ctx, opts...)
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

func (c *GCPClient) GetNEG(ctx context.Context, name string) (*computepb.NetworkEndpointGroup, error) {
	req := &computepb.GetRegionNetworkEndpointGroupRequest{
		Project:              c.cfg.Project,
		Region:               c.cfg.Region,
		NetworkEndpointGroup: name,
	}
	return c.negs.Get(ctx, req, callOpts()...)
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

func (c *GCPClient) GetFirewallPolicies(ctx context.Context, name string) (*computepb.FirewallPolicy, error) {
	req := &computepb.GetRegionNetworkFirewallPolicyRequest{
		FirewallPolicy: name,
	}
	return c.firewalls.Get(ctx, req, callOpts()...)
}

func (c *GCPClient) CreateFirewallPolicies(ctx context.Context, name string, ports []int32, instances []string) error {
	reqID := uuid.New().String()
	rule := firewallRule(ports, instances)
	req := &computepb.InsertRegionNetworkFirewallPolicyRequest{
		RequestId: &reqID,
		Project:   c.cfg.Project,
		Region:    c.cfg.Region,
		FirewallPolicyResource: &computepb.FirewallPolicy{
			Name:  &name,
			Rules: []*computepb.FirewallPolicyRule{rule},
		},
	}
	return call(ctx, c.firewalls.Insert, req)
}

func (c *GCPClient) UpdateFirewallPolicies(ctx context.Context, name string, ports []int32, instances []string) error {
	reqID := uuid.New().String()
	rule := firewallRule(ports, instances)
	req := &computepb.PatchRegionNetworkFirewallPolicyRequest{
		RequestId: &reqID,
		Project:   c.cfg.Project,
		Region:    c.cfg.Region,
		FirewallPolicyResource: &computepb.FirewallPolicy{
			Name:  &name,
			Rules: []*computepb.FirewallPolicyRule{rule},
		},
	}
	return call(ctx, c.firewalls.Patch, req)
}

func (c *GCPClient) GetBackendService(ctx context.Context, name string) (*computepb.BackendService, error) {
	req := &computepb.GetRegionBackendServiceRequest{
		Project:        c.cfg.Project,
		Region:         c.cfg.Region,
		BackendService: name,
	}
	return c.backendSvcs.Get(ctx, req, callOpts()...)
}

func (c *GCPClient) CreateBackendService(ctx context.Context, name string, neg string) error {
	reqID := uuid.New().String()
	protocol := computepb.BackendService_TCP.String()
	req := &computepb.InsertRegionBackendServiceRequest{
		RequestId: &reqID,
		Project:   c.cfg.Project,
		Region:    c.cfg.Region,
		BackendServiceResource: &computepb.BackendService{
			Name:     &name,
			Network:  &c.cfg.Network,
			Protocol: &protocol,
			Backends: []*computepb.Backend{{
				Group: &neg,
				// TODO:
				// MaxConnections, etc.
			}},
		},
	}
	return call(ctx, c.backendSvcs.Insert, req)
}

func (c *GCPClient) GetForwardingRule(ctx context.Context, name string) (*computepb.ForwardingRule, error) {
	req := &computepb.GetForwardingRuleRequest{
		Project:        c.cfg.Project,
		Region:         c.cfg.Region,
		ForwardingRule: name,
	}
	return c.fwdRules.Get(ctx, req, callOpts()...)
}

func (c *GCPClient) CreateForwardingRule(ctx context.Context, name, backendSvc string, ip *string, globalAccess *bool, ports []int32) error {
	reqID := uuid.New().String()
	scheme := computepb.BackendService_INTERNAL.String()
	strPorts := toStr(ports)
	req := &computepb.InsertForwardingRuleRequest{
		RequestId: &reqID,
		Project:   c.cfg.Project,
		Region:    c.cfg.Region,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name:                &name,
			IPAddress:           ip,
			AllowGlobalAccess:   globalAccess,
			BackendService:      &backendSvc,
			Network:             &c.cfg.Network,
			Subnetwork:          &c.cfg.Subnetwork,
			Ports:               strPorts,
			LoadBalancingScheme: &scheme,
		},
	}
	return call(ctx, c.fwdRules.Insert, req)
}

func (c *GCPClient) GetServiceAttachment(ctx context.Context, name string) (*computepb.ServiceAttachment, error) {
	req := &computepb.GetServiceAttachmentRequest{
		Project:           c.cfg.Project,
		Region:            c.cfg.Region,
		ServiceAttachment: name,
	}
	return c.svcAtts.Get(ctx, req, callOpts()...)
}

func (c *GCPClient) CreateServiceAttachment(
	ctx context.Context,
	name,
	fwdRuleFQN string,
	consumers []*computepb.ServiceAttachmentConsumerProjectLimit,
	natSubnetFQNs []string,
) error {
	reqID := uuid.New().String()
	req := &computepb.InsertServiceAttachmentRequest{
		RequestId: &reqID,
		Project:   c.cfg.Project,
		Region:    c.cfg.Region,
		ServiceAttachmentResource: &computepb.ServiceAttachment{
			Name:                   &name,
			ProducerForwardingRule: &fwdRuleFQN,
			ConsumerAcceptLists:    consumers,
			NatSubnets:             natSubnetFQNs,
		},
	}
	return call(ctx, c.svcAtts.Insert, req)
}

func (c *GCPClient) ForwardingRuleFQN(name string) string {
	return "projects/" + c.cfg.Project + "/regions/" + c.cfg.Region + "/forwardingRules/" + name
}

func firewallRule(ports []int32, instances []string) *computepb.FirewallPolicyRule {
	allow := "allow"
	tcp := "tcp"
	priority := int32(1000)
	ingress := computepb.FirewallPolicyRule_INGRESS.String()
	strPorts := toStr(ports)
	return &computepb.FirewallPolicyRule{
		Action:          &allow,
		Direction:       &ingress,
		TargetResources: instances,
		Priority:        &priority,
		Match: &computepb.FirewallPolicyRuleMatcher{
			Layer4Configs: []*computepb.FirewallPolicyRuleMatcherLayer4Config{{
				IpProtocol: &tcp,
				Ports:      strPorts,
			}},
		},
	}
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
	if err != nil {
		var ae *apierror.APIError
		if errors.As(err, &ae) {
			if ae.HTTPCode() != http.StatusNotFound {
				return u, ErrNotFound
			}
			return u, &ClientError{msg: ae.Error(), status: ae.HTTPCode()}
		}
		return u, &ClientError{msg: err.Error(), status: -1}
	}
	return u, nil
}

func call[T any, F func(context.Context, T, ...gax.CallOption) (*compute.Operation, error)](ctx context.Context, f F, req T) error {
	op, err := f(ctx, req)
	if err != nil {
		return err
	}
	return op.Wait(ctx, callOpts()...)
}

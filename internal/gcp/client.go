package gcp

import (
	"context"

	"cloud.google.com/go/compute/apiv1/computepb"
)

type Client interface {
	// NEGs API
	GetNEG(ctx context.Context, name string) (*computepb.NetworkEndpointGroup, error)
	CreatePortmapNEG(ctx context.Context, name string) error
	AttachEndpoint(ctx context.Context, neg, instance string, port, instancePort int32) error
	// Firewalls API
	GetFirewallPolicies(ctx context.Context, name string) (*computepb.FirewallPolicy, error)
	CreateFirewallPolicies(ctx context.Context, name string, ports []int32, instances []string) error
	// Backend Services API
	GetBackendService(ctx context.Context, name string) (*computepb.BackendService, error)
	CreateBackendService(ctx context.Context, name string, neg string) error
	// Forwarding Rules API
	GetForwardingRule(ctx context.Context, name string) (*computepb.ForwardingRule, error)
	CreateForwardingRule(ctx context.Context, name, backendSvc, ip string, globalAccess bool, ports []string) error
}

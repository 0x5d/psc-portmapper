package gcp

import (
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/stretchr/testify/assert"
)

func TestFirewallNeedsUpdate(t *testing.T) {
	tests := []struct {
		name          string
		fw            func() *computepb.FirewallPolicy
		expectedPorts map[int32]struct{}
		expected      bool
	}{{
		name:     "Firewall is nil",
		fw:       func() *computepb.FirewallPolicy { return nil },
		expected: true,
	}, {
		name: "Firewall has no rules",
		fw: func() *computepb.FirewallPolicy {
			fw := FirewallPolicy()
			fw.Rules = []*computepb.FirewallPolicyRule{}
			return fw
		},
		expected: true,
	}, {
		name: "Firewall rule is nil",
		fw: func() *computepb.FirewallPolicy {
			fw := FirewallPolicy()
			fw.Rules = []*computepb.FirewallPolicyRule{nil}
			return fw
		},
		expected: true,
	}, {
		name: "Firewall rule match is nil",
		fw: func() *computepb.FirewallPolicy {
			fw := FirewallPolicy()
			fw.Rules = []*computepb.FirewallPolicyRule{{Match: nil}}
			return fw
		},
		expected: true,
	}, {
		name: "Firewall rule match has no Layer4Configs",
		fw: func() *computepb.FirewallPolicy {
			fw := FirewallPolicy()
			fw.Rules[0].Match.Layer4Configs = []*computepb.FirewallPolicyRuleMatcherLayer4Config{}
			return fw
		},
		expected: true,
	}, {
		name: "Firewall rule match Layer4Config is nil",
		fw: func() *computepb.FirewallPolicy {
			fw := FirewallPolicy()
			fw.Rules[0].Match.Layer4Configs = []*computepb.FirewallPolicyRuleMatcherLayer4Config{nil}
			return fw
		},
		expected: true,
	}, {
		name: "Firewall rule match Layer4Config IpProtocol is nil",
		fw: func() *computepb.FirewallPolicy {
			fw := FirewallPolicy()
			fw.Rules[0].Match.Layer4Configs[0].IpProtocol = nil
			return fw
		},
		expected: true,
	}, {
		name: "Firewall rule match Layer4Config IpProtocol is not tcp",
		fw: func() *computepb.FirewallPolicy {
			fw := FirewallPolicy()
			fw.Rules[0].Match.Layer4Configs[0].IpProtocol = stringPtr("udp")
			return fw
		},
		expected: true,
	}, {
		name: "Firewall rule match Layer4Config Ports does not have ports",
		fw: func() *computepb.FirewallPolicy {
			fw := FirewallPolicy()
			fw.Rules[0].Match.Layer4Configs[0].Ports = nil
			return fw
		},
		expectedPorts: map[int32]struct{}{80: {}},
		expected:      true,
	}, {
		name: "Firewall rule match Layer4Config Ports do not match expected ports",
		fw: func() *computepb.FirewallPolicy {
			fw := FirewallPolicy()
			fw.Rules[0].Match.Layer4Configs[0].Ports = []string{"81"}
			return fw
		},
		expectedPorts: map[int32]struct{}{80: {}},
		expected:      true,
	}, {
		name:          "Firewall rule match Layer4Config Ports match expected ports",
		fw:            FirewallPolicy,
		expectedPorts: map[int32]struct{}{80: {}},
		expected:      false,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			update := FirewallNeedsUpdate(tt.fw(), tt.expectedPorts)
			assert.Equal(t, tt.expected, update)
		})
	}
}

func FirewallPolicy() *computepb.FirewallPolicy {
	return &computepb.FirewallPolicy{
		Rules: []*computepb.FirewallPolicyRule{{
			Match: &computepb.FirewallPolicyRuleMatcher{
				Layer4Configs: []*computepb.FirewallPolicyRuleMatcherLayer4Config{{
					IpProtocol: stringPtr("tcp"),
					Ports:      []string{"80"},
				}},
			}},
		},
	}
}

func stringPtr(s string) *string {
	return &s
}

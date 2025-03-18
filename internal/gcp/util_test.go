package gcp

import (
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/stretchr/testify/assert"
)

func TestFirewallNeedsUpdate(t *testing.T) {
	tests := []struct {
		name          string
		fw            func() *computepb.Firewall
		expectedPorts map[int32]struct{}
		expected      bool
	}{{
		name:     "Firewall is nil",
		fw:       func() *computepb.Firewall { return nil },
		expected: true,
	}, {
		name: "Firewall has no rules",
		fw: func() *computepb.Firewall {
			fw := Firewall()
			fw.Allowed = []*computepb.Allowed{}
			return fw
		},
		expected: true,
	}, {
		name: "Firewall rule is nil",
		fw: func() *computepb.Firewall {
			fw := Firewall()
			fw.Allowed = []*computepb.Allowed{nil}
			return fw
		},
		expected: true,
	}, {
		name: "Firewall IPProtocol is nil",
		fw: func() *computepb.Firewall {
			fw := Firewall()
			fw.Allowed[0].IPProtocol = nil
			return fw
		},
		expected: true,
	}, {
		name: "Firewall IPProtocol is not tcp",
		fw: func() *computepb.Firewall {
			fw := Firewall()
			fw.Allowed[0].IPProtocol = stringPtr("udp")
			return fw
		},
		expected: true,
	}, {
		name: "Firewall does not have ports",
		fw: func() *computepb.Firewall {
			fw := Firewall()
			fw.Allowed[0].Ports = nil
			return fw
		},
		expectedPorts: map[int32]struct{}{80: {}},
		expected:      true,
	}, {
		name: "Firewall Ports do not match expected ports",
		fw: func() *computepb.Firewall {
			fw := Firewall()
			fw.Allowed[0].Ports = []string{"81"}
			return fw
		},
		expectedPorts: map[int32]struct{}{80: {}},
		expected:      true,
	}, {
		name:          "Firewall Ports match expected ports",
		fw:            Firewall,
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

func Firewall() *computepb.Firewall {
	return &computepb.Firewall{
		Allowed: []*computepb.Allowed{{
			IPProtocol: stringPtr("tcp"),
			Ports:      []string{"80"},
		}},
	}
}

func stringPtr(s string) *string {
	return &s
}

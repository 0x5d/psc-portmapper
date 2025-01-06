package controller

import (
	"testing"

	"github.com/0x5d/psc-portmapper/internal/gcp"
	"github.com/stretchr/testify/require"
)

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

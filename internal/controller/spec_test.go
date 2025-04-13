package controller

import (
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"
)

func TestParseSpec(t *testing.T) {
	tests := []struct {
		name         string
		jsonSpec     string
		expectedErr  string
		expectedSpec *Spec
	}{{
		name:        "Fails if JSON is invalid",
		jsonSpec:    `{"prefix": "test",`,
		expectedErr: "couldn't decode the spec from JSON: unexpected end of JSON input",
	}, {
		name:        "Fails if spec is invalid",
		jsonSpec:    `{"nat_subnet_fqns": []}`,
		expectedErr: "invalid spec: nat_subnet_fqns is empty",
	}, {
		name: "Parses valid spec with NetworkFQN",
		jsonSpec: `{
				"nat_subnet_fqns": ["projects/my-project-123/regions/us-east1/subnetworks/my-subnet"],
				"consumer_accept_list": [{
					"network_fqn": "projects/my-project-123/global/networks/my-vpc",
					"connection_limit": 10
				}]
			}`,
		expectedSpec: &Spec{
			NatSubnetFQNs: []string{"projects/my-project-123/regions/us-east1/subnetworks/my-subnet"},
			ConsumerAcceptList: []*Consumer{{
				NetworkFQN:      stringPtr("projects/my-project-123/global/networks/my-vpc"),
				ConnectionLimit: 10,
			}},
		},
	}, {
		name: "Parses valid spec with ProjectIdOrNum",
		jsonSpec: `{
				"nat_subnet_fqns": ["projects/my-project-123/regions/us-east1/subnetworks/my-subnet"],
				"consumer_accept_list": [{
					"project_id_or_num": "project1",
					"connection_limit": 10
				}]
			}`,
		expectedSpec: &Spec{
			NatSubnetFQNs: []string{"projects/my-project-123/regions/us-east1/subnetworks/my-subnet"},
			ConsumerAcceptList: []*Consumer{{
				ProjectIdOrNum:  stringPtr("project1"),
				ConnectionLimit: 10,
			}},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := testr.New(t)
			spec, err := parseSpec(log, tt.jsonSpec)
			if tt.expectedErr != "" {
				require.EqualError(t, err, tt.expectedErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expectedSpec, spec)
		})
	}
}

func TestValidateSpec(t *testing.T) {
	tests := []struct {
		name        string
		spec        *Spec
		expectedErr string
	}{{
		name:        "Fails if the spec is nil",
		expectedErr: "spec is nil",
	}, {
		name: "Returns no errors for a spec with only NetworkFQN",
		spec: &Spec{
			NatSubnetFQNs: []string{"projects/my-project-123/regions/us-east1/subnetworks/my-subnet"},
			ConsumerAcceptList: []*Consumer{{
				NetworkFQN:      stringPtr("projects/my-project-123/global/networks/my-vpc"),
				ConnectionLimit: 10,
			}},
		},
	}, {
		name: "Returns no errors for a spec with only ProjectIdOrNum",
		spec: &Spec{
			NatSubnetFQNs:      []string{"projects/my-project-123/regions/us-east1/subnetworks/my-subnet"},
			ConsumerAcceptList: []*Consumer{{ProjectIdOrNum: stringPtr("project1"), ConnectionLimit: 10}},
		},
	}, {
		name: "Fails if NetworkFQN is invalid",
		spec: &Spec{
			NatSubnetFQNs:      []string{"projects/my-project-123/regions/us-east1/subnetworks/my-subnet"},
			ConsumerAcceptList: []*Consumer{{NetworkFQN: stringPtr("net")}},
		},
		expectedErr: "invalid value for network_fqn (\"net\") in consumer_list[0], expected format: projects/<project-id>/global/networks/<network-name>",
	}, {
		name: "Fails if both NetworkFQN and ProjectIdOrNum are set",
		spec: &Spec{
			NatSubnetFQNs: []string{"projects/my-project-123/regions/us-east1/subnetworks/my-subnet"},
			ConsumerAcceptList: []*Consumer{{
				NetworkFQN:      stringPtr("projects/my-project-123/global/networks/my-vpc"),
				ProjectIdOrNum:  stringPtr("project1"),
				ConnectionLimit: 10,
			}},
		},
		expectedErr: "network_fqn and project_id_or_num can't both be set in consumer_list[0]",
	}, {
		name: "Fails if neither NetworkFQN nor ProjectIdOrNum are set",
		spec: &Spec{
			NatSubnetFQNs:      []string{"projects/my-project-123/regions/us-east1/subnetworks/my-subnet"},
			ConsumerAcceptList: []*Consumer{{ConnectionLimit: 10}},
		},
		expectedErr: "either network_fqn or project_id_or_num must be set in consumer_list[0]",
	}, {
		name: "It's OK if ConnectionLimit is not set",
		spec: &Spec{
			NatSubnetFQNs:      []string{"projects/my-project-123/regions/us-east1/subnetworks/my-subnet"},
			ConsumerAcceptList: []*Consumer{{ProjectIdOrNum: stringPtr("my-project")}},
		},
	}, {
		name:        "Fails if a NatSubnetFQNs is empty",
		spec:        &Spec{},
		expectedErr: "nat_subnet_fqns is empty",
	}, {
		name: "Fails if a NatSubnetFQN is invalid",
		spec: &Spec{
			NatSubnetFQNs: []string{"subnet", "projects/my-project-123/regions/us-east1//my-subnet"},
		},
		expectedErr: "invalid value for nat_subnet_fqns[0] (\"subnet\"), expected format: projects/<project-id>/regions/<region-name>/subnetworks/<subnetwork-name>; invalid value for nat_subnet_fqns[1] (\"projects/my-project-123/regions/us-east1//my-subnet\"), expected format: projects/<project-id>/regions/<region-name>/subnetworks/<subnetwork-name>",
	}, {
		name: "Accumulates errors",
		spec: &Spec{
			NatSubnetFQNs: []string{"projects/my-project-123/regions/us-east1/subnetworks/my-subnet"},
			ConsumerAcceptList: []*Consumer{
				{ProjectIdOrNum: stringPtr("my-project"), NetworkFQN: stringPtr("projects/my-project-123/global/networks/my-vpc")},
				{ConnectionLimit: 0},
				{NetworkFQN: stringPtr("net")},
			},
		},
		expectedErr: "network_fqn and project_id_or_num can't both be set in consumer_list[0]; either network_fqn or project_id_or_num must be set in consumer_list[1]; invalid value for network_fqn (\"net\") in consumer_list[2], expected format: projects/<project-id>/global/networks/<network-name>",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := testr.New(t)
			err := validateSpec(log, tt.spec)
			if tt.expectedErr != "" {
				require.EqualError(t, err, tt.expectedErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func stringPtr(s string) *string {
	return &s
}

package controller

import (
	"fmt"
	"regexp"

	"github.com/go-logr/logr"
	"go.uber.org/multierr"
)

// Spec is the configuration for the controller, which is loaded from an annotation on the
// StatefulSet.
type Spec struct {
	Prefix             string                `json:"prefix"`
	IP                 *string               `json:"ip,omitempty"`
	GlobalAccess       *bool                 `json:"global_access,omitempty"`
	ConsumerAcceptList []*Consumer           `json:"consumer_accept_list,omitempty"`
	NatSubnetFQNs      []string              `json:"nat_subnet_fqns,omitempty"`
	NodePorts          map[string]PortConfig `json:"node_ports"`
}

// See https://cloud.google.com/compute/docs/reference/rest/v1/serviceAttachments
type Consumer struct {
	NetworkFQN      *string `json:"network_fqn,omitempty"`
	ConnectionLimit uint32  `json:"connection_limit,omitempty"`
	ProjectIdOrNum  *string `json:"project_id_or_num,omitempty"`
}

type PortConfig struct {
	NodePort      int32 `json:"node_port"`
	ContainerPort int32 `json:"container_port"`
	StartingPort  int32 `json:"starting_port"`
}

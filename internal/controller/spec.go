package controller

import (
	"encoding/json"
	"errors"
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

// networkFQNRegexp matches the format of a network FQN, e.g.
// projects/my-project-id/global/networks/my-vpc-name
var networkFQNRegexp = regexp.MustCompile(`^projects\/[^/]+\/global\/networks\/[^/]+$`)

// subnetFQNRegexp matches the format of a subnet FQN, e.g.
// projects/my-project-id/regions/us-east1/subnetworks/my-subnet-name
var subnetFQNRegexp = regexp.MustCompile(`^projects\/[^/]+\/regions\/[^/]+\/subnetworks\/[^/]+$`)

func parseSpec(log logr.Logger, jsonSpec string) (*Spec, error) {
	var spec Spec
	err := json.Unmarshal([]byte(jsonSpec), &spec)
	if err != nil {
		return nil, fmt.Errorf("couldn't decode the spec from JSON: %w", err)
	}

	err = validateSpec(log, &spec)
	if err != nil {
		return nil, fmt.Errorf("invalid spec: %w", err)
	}
	return &spec, nil
}

func validateSpec(log logr.Logger, spec *Spec) error {
	if spec == nil {
		return fmt.Errorf("spec is nil")
	}

	if len(spec.ConsumerAcceptList) == 0 {
		log.Info("consumer_accept_list is empty, no incoming connections will be allowed.")
	}

	var err error
	for i, c := range spec.ConsumerAcceptList {
		if c.NetworkFQN == nil && c.ProjectIdOrNum == nil {
			err = multierr.Append(err, fmt.Errorf("either network_fqn or project_id_or_num must be set in consumer_list[%d]", i))
		}
		if c.NetworkFQN != nil && c.ProjectIdOrNum != nil {
			err = multierr.Append(err, fmt.Errorf("network_fqn and project_id_or_num can't both be set in consumer_list[%d]", i))
		}
		if c.NetworkFQN != nil {
			matches := networkFQNRegexp.FindStringSubmatch(*c.NetworkFQN)
			if matches == nil {
				matchErr := fmt.Errorf(
					"invalid value for network_fqn (%q) in consumer_list[%d], expected format: projects/<project-id>/global/networks/<network-name>",
					*c.NetworkFQN,
					i,
				)
				err = multierr.Append(err, matchErr)
			}
		}
		if c.ConnectionLimit == 0 {
			log.Info(
				"connection_limit is not set, no connections will be allowed from it.",
				"network_fqn", c.NetworkFQN,
				"project_id_or_num", c.ProjectIdOrNum,
			)
		}
	}

	if len(spec.NatSubnetFQNs) == 0 {
		err = multierr.Append(err, errors.New("nat_subnet_fqns is empty"))
	}
	for i, sn := range spec.NatSubnetFQNs {
		matches := subnetFQNRegexp.FindStringSubmatch(sn)
		if matches == nil {
			matchErr := fmt.Errorf(
				"invalid value for nat_subnet_fqns[%d] (%q), expected format: projects/<project-id>/regions/<region-name>/subnetworks/<subnetwork-name>",
				i,
				sn,
			)
			err = multierr.Append(err, matchErr)
		}
	}

	return err
}

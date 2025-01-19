package controller

// Spec is the configuration for the controller, which is loaded from an annotation on the
// StatefulSet.
type Spec struct {
	Prefix    string                `json:"prefix"`
	NodePorts map[string]PortConfig `json:"node_ports"`
}

type PortConfig struct {
	NodePort      int32 `json:"node_port"`
	ContainerPort int32 `json:"container_port"`
	StartingPort  int32 `json:"starting_port"`
}

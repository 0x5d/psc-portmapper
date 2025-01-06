package controller

// Spec is the configuration for the controller, which is loaded from an annotation on the
// StatefulSet.
type Spec struct {
	Prefix    string `json:"prefix"`
	StartPort int32  `json:"start_port"`
}

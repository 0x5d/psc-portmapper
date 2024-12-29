package gcp

type ClientConfig struct {
	Project     string            `env:"PROJECT"`
	Region      string            `env:"REGION"`
	Network     string            `env:"NETWORK"`
	Subnetwork  string            `env:"SUBNET"`
	Annotations map[string]string `env:"ANNOTATIONS"`
}

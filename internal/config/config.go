package config

import "github.com/0x5d/psc-portmapper/internal/gcp"

type Config struct {
	GCP *gcp.ClientConfig `env:", prefix=GCP_"`
}

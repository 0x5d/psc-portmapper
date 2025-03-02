# psc-portmapper

A Kubernetes controller to automatically expose a statefulset through GCP Private Service Connect, using Port Mapping NEGs.

This chart installs a psc-portmapper deployment on a [Kubernetes](http://kubernetes.io) cluster using [Helm](https://helm.sh).

## Prerequisites

- Kubernetes 1.19+
- Helm 3.7+

## Install

Go to the [releases page](https://github.com/0x5d/psc-portmapper/releases) to check the list of releases.

The only required fields without default values are those under `config.gcp`, which are needed for psc-portmapper to be able to deploy and manage the GCP resources.

You can find a list of available configuration values [here](https://github.com/0x5d/psc-portmapper/blob/main/charts/psc-portmapper/values.yaml).

```bash
VERSION=0.1.0
cat << EOF > values.yml
config:
  gcp:
    project: ""
    region: ""
    network: ""
    subnet: ""
EOF
helm install psc-portmapper -f values.yaml https://github.com/0x5d/psc-portmapper/archive/refs/tags/$VERSION.tar.gz
```

Make sure you grant the required GCP roles to the service account created by the Helm chart. Learn more [here](https://cloud.google.com/kubernetes-engine/docs/how-to/workload-identity).
 
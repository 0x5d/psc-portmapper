# psc-portmapper

A Kubernetes controller to automatically expose a statefulset through GCP Private Service Connect, using Port Mapping NEGs.

See the [design doc](design.md) for more info.

## IMPORTANT

This is a WIP, currently undergoing testing and lots of changes. Contributions are very much welcome.

## Reqirements

`psc-portmapper` requires its target statefulset to keep a 1:1 pod-node relationship, to be able to map a single port (i.e. on the forwarding rule exposed through the service attachment) to a single pod. This is because it creates a NodePort service to satisfy the port-mapping Network Endpoint Group's requirement to have an instance:port pair as its target.

Because of this, `psc-portmapper` isn't compatible with [Autopilot clusters](https://cloud.google.com/kubernetes-engine/docs/concepts/autopilot-overview), as they don't create actual instances which can be used as endpoint targets.

## Installation

### Helm (Recommended)

See the [Chart docs](charts/psc-portmapper/readme.md).

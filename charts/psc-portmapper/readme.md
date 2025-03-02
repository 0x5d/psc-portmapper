# psc-portmapper

A Kubernetes controller to automatically expose a statefulset through GCP Private Service Connect, using Port Mapping NEGs.

This chart installs a psc-portmapper deployment on a [Kubernetes](http://kubernetes.io) cluster using [Helm](https://helm.sh).

## Prerequisites

- Kubernetes 1.19+
- Helm 3.7+

## Add repository

```console
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update
```

## Install

```console
helm install [RELEASE_NAME] prometheus-community/prometheus
```
# VPC
resource "google_compute_network" "vpc" {
  name                    = var.vpc_name
  auto_create_subnetworks = false
}

# Subnet
resource "google_compute_subnetwork" "subnet" {
  name             = var.subnet_name
  region           = var.region
  network          = google_compute_network.vpc.id
  ip_cidr_range    = var.subnet_cidr
  stack_type       = "IPV4_IPV6"
  ipv6_access_type = "EXTERNAL"

  # Secondary IP ranges for GKE pods and services
  secondary_ip_range {
    range_name    = "${var.subnet_name}-pod-range"
    ip_cidr_range = var.pod_cidr
  }

  secondary_ip_range {
    range_name    = "${var.subnet_name}-svc-range"
    ip_cidr_range = var.svc_cidr
  }
}

resource "google_compute_subnetwork" "nat_subnet" {
  name          = "${var.subnet_name}-nat"
  region        = var.region
  network       = google_compute_network.vpc.id
  ip_cidr_range = "10.0.1.0/24"
  purpose       = "PRIVATE_SERVICE_CONNECT"
}

# GKE cluster
resource "google_container_cluster" "default" {
  name = "psc-portmapper"

  location                 = var.region
  enable_autopilot         = true
  enable_l4_ilb_subsetting = true
  workload_identity_config {
    workload_pool = "${var.project_id}.svc.id.goog"
  }

  network    = google_compute_network.vpc.id
  subnetwork = google_compute_subnetwork.subnet.id

  ip_allocation_policy {
    stack_type                    = "IPV4_IPV6"
    services_secondary_range_name = google_compute_subnetwork.subnet.secondary_ip_range[0].range_name
    cluster_secondary_range_name  = google_compute_subnetwork.subnet.secondary_ip_range[1].range_name
  }

  # Set `deletion_protection` to `true` will ensure that one cannot
  # accidentally delete this instance by use of Terraform.
  deletion_protection = false
}

resource "google_service_account" "svc_acc" {
  project      = var.project_id
  account_id   = "psc-portmapper"
  display_name = "psc-portmapper"
}

resource "google_project_iam_member" "project" {
  project = var.project_id
  role    = google_project_iam_custom_role.psc_portmapper_role.name
  member  = "serviceAccount:${google_service_account.svc_acc.email}"
}

resource "google_service_account_iam_binding" "svc_acc_workload_identity_user_role_binding" {
  service_account_id = google_service_account.svc_acc.name
  role               = google_project_iam_custom_role.psc_portmapper_role.name

  members = [
    "serviceAccount:${google_service_account.svc_acc.email}",
  ]
}

resource "google_service_account_iam_binding" "svc_acc_custom_role_binding" {
  service_account_id = google_service_account.svc_acc.name
  role               = "roles/iam.workloadIdentityUser"

  members = [
    "serviceAccount:${var.project_id}.svc.id.goog[${var.namespace}/psc-portmapper]",
  ]
}

resource "google_project_iam_custom_role" "psc_portmapper_role" {
  role_id = "pscPortmapper"
  title   = "psc-portmapper"
  permissions = [
    "compute.forwardingRules.create",
    "compute.forwardingRules.delete",
    "compute.forwardingRules.get",
    "compute.forwardingRules.list",
    "compute.forwardingRules.pscCreate",
    "compute.forwardingRules.pscDelete",
    "compute.forwardingRules.pscSetLabels",
    "compute.forwardingRules.pscSetTarget",
    "compute.forwardingRules.pscUpdate",
    "compute.forwardingRules.setTarget",
    "compute.forwardingRules.update",
    "compute.forwardingRules.use",
    "compute.networks.getRegionEffectiveFirewalls",
    "compute.networks.setFirewallPolicy",
    "compute.networks.updatePolicy",
    "compute.regionBackendServices.create",
    "compute.regionBackendServices.delete",
    "compute.regionBackendServices.get",
    "compute.regionBackendServices.list",
    "compute.regionBackendServices.update",
    "compute.regionBackendServices.use",
    "compute.regionFirewallPolicies.create",
    "compute.regionFirewallPolicies.delete",
    "compute.regionFirewallPolicies.get",
    "compute.regionFirewallPolicies.list",
    "compute.regionFirewallPolicies.update",
    "compute.regionFirewallPolicies.use",
    "compute.regionNetworkEndpointGroups.attachNetworkEndpoints",
    "compute.regionNetworkEndpointGroups.create",
    "compute.regionNetworkEndpointGroups.delete",
    "compute.regionNetworkEndpointGroups.detachNetworkEndpoints",
    "compute.regionNetworkEndpointGroups.get",
    "compute.regionNetworkEndpointGroups.list",
    "compute.regionNetworkEndpointGroups.use",
    "compute.regionOperations.get",
    "compute.regionOperations.list",
    "compute.serviceAttachments.create",
    "compute.serviceAttachments.delete",
    "compute.serviceAttachments.get",
    "compute.serviceAttachments.list",
    "compute.serviceAttachments.update",
    "compute.serviceAttachments.use",
  ]
}

// kubernetes_manifest would be better (to keep TF files uncluttered), but it requires that the cluster be up and running.
// See https://github.com/hashicorp/terraform-provider-kubernetes/issues/1391
resource "kubernetes_stateful_set" "nginx" {
  depends_on = [google_container_cluster.default]
  timeouts {
    create = "5m"
    update = "2m"
    delete = "2m"
  }
  metadata {
    name      = "nginx"
    namespace = var.namespace
    annotations = {
      "psc-portmapper.0x5d.org/spec" : jsonencode({ "prefix" : "prefix-", "nat_subnet_fqns" : ["${google_compute_subnetwork.nat_subnet.id}"], "node_ports" : { "web" : { "node_port" : 30000, "container_port" : 8080, "starting_port" : 30000 } } })
    }
  }

  spec {
    service_name      = "nginx"
    replicas          = 3
    min_ready_seconds = 5
    selector {
      match_labels = {
        "app" : "nginx",
      }
    }
    template {
      metadata {
        labels = {
          "app" : "nginx",
        }
      }
      spec {
        termination_grace_period_seconds = 10
        affinity {
          pod_anti_affinity {
            required_during_scheduling_ignored_during_execution {
              topology_key = "kubernetes.io/hostname"
              label_selector {
                match_expressions {
                  key      = "app"
                  operator = "In"
                  values   = ["nginx"]
                }
              }
            }
          }
        }
        container {
          name  = "nginx"
          image = "registry.k8s.io/nginx-slim:0.24"
          port {
            name           = "web"
            container_port = 80
          }
          resources {
            limits = {
              cpu    = "500m"
              memory = "512Mi"
              ephemeral-storage = "1Gi"
            }
            requests = {
              cpu    = "500m"
              memory = "512Mi"
              ephemeral-storage = "1Gi"
            }
          }
        }
      }
    }
  }
}

resource "helm_release" "psc_controller" {
  name         = "psc-portmapper"
  chart        = "${path.module}/../charts/psc-portmapper"
  namespace    = var.namespace
  atomic       = false
  set {
    name  = "config.gcp.project"
    value = var.project_id
  }
  set {
    name  = "config.gcp.region"
    value = var.region
  }
  set {
    name  = "config.gcp.network"
    value = google_compute_network.vpc.name
  }
  set {
    name  = "config.gcp.subnet"
    value = google_compute_subnetwork.subnet.name
  }
  set {
    name  = "config.gcp.serviceAccount"
    value = google_service_account.svc_acc.email
  }
  set {
    name  = "templates_hash"
    value = sha1(join("", [for f in fileset("${path.module}/../charts/psc-portmapper/templates", "*") : filesha1("${path.module}/../charts/psc-portmapper/templates/${f}")]))
  }
}

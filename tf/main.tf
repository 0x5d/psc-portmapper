// kubernetes_manifest would be better (to keep TF files uncluttered), but it requires that the cluster be up and running.
// See https://github.com/hashicorp/terraform-provider-kubernetes/issues/1391
resource "kubernetes_stateful_set" "nginx" {
  depends_on = [google_container_node_pool.primary_nodes]
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
              cpu               = "500m"
              memory            = "512Mi"
              ephemeral-storage = "1Gi"
            }
            requests = {
              cpu               = "500m"
              memory            = "512Mi"
              ephemeral-storage = "1Gi"
            }
          }
        }
      }
    }
  }
}

resource "helm_release" "psc_controller" {
  depends_on = [google_container_node_pool.primary_nodes]
  name       = "psc-portmapper"
  chart      = "${path.module}/../charts/psc-portmapper"
  namespace  = var.namespace
  atomic     = false
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

resource "google_container_cluster" "primary" {
  name     = "psc-portmapper"
  location = var.region

  # We can't create a cluster with no node pool defined, but we want to only use
  # separately managed node pools. So we create the smallest possible default
  # node pool and immediately delete it.
  remove_default_node_pool = true
  initial_node_count       = 1

  network             = google_compute_network.vpc.name
  subnetwork          = google_compute_subnetwork.subnet.name
  deletion_protection = false
  ip_allocation_policy {
    cluster_secondary_range_name  = "${var.subnet_name}-pod-range"
    services_secondary_range_name = "${var.subnet_name}-svc-range"
  }
  workload_identity_config {
    workload_pool = "${var.project_id}.svc.id.goog"
  }
}

resource "google_container_node_pool" "primary_nodes" {
  name     = google_container_cluster.primary.name
  location = var.region
  cluster  = google_container_cluster.primary.name

  version    = data.google_container_engine_versions.gke_version.release_channel_default_version["STABLE"]
  node_count = var.node_count

  node_config {
    oauth_scopes = [
      "https://www.googleapis.com/auth/logging.write",
      "https://www.googleapis.com/auth/monitoring",
    ]

    labels = {
      env = var.project_id
    }

    # preemptible  = true
    tags         = ["gke-node", "${var.project_id}-gke"]
    machine_type = var.machine_type
    disk_size_gb = var.disk_size_gb
    disk_type    = var.disk_type
    metadata = {
      disable-legacy-endpoints = "true"
    }
  }
}

data "google_container_engine_versions" "gke_version" {
  location       = var.region
  version_prefix = "1.27."
}
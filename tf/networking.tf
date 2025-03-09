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
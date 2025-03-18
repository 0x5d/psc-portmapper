variable "project_id" {
  description = "The project ID to deploy resources into"
  type        = string
}

variable "region" {
  description = "The region to deploy resources into"
  type        = string
  default     = "us-central1"
}

variable "vpc_name" {
  description = "The name of the VPC network"
  type        = string
  default     = "gke-vpc"
}

variable "subnet_name" {
  description = "The name of the subnet"
  type        = string
  default     = "gke-subnet"
}

variable "subnet_cidr" {
  description = "The CIDR for the subnet"
  type        = string
  default     = "10.0.0.0/24"
}

variable "pod_cidr" {
  description = "The CIDR for pods"
  type        = string
  default     = "10.1.0.0/16"
}

variable "svc_cidr" {
  description = "The CIDR for services"
  type        = string
  default     = "10.2.0.0/16"
}

variable "cluster_name" {
  description = "The name of the GKE cluster"
  type        = string
  default     = "gke-cluster"
}

variable "node_count" {
  description = "The number of nodes per zone in the GKE cluster"
  type        = number
  default     = 1
}

variable "machine_type" {
  description = "The machine type for GKE nodes"
  type        = string
  default     = "n1-standard-1"
}

variable "disk_size_gb" {
  description = "The disk size for GKE nodes in GB"
  type        = number
  default     = 50
}

variable "disk_type" {
  description = "The disk type for GKE nodes"
  type        = string
  default     = "pd-standard"
}

variable "namespace" {
  description = "The namespace to deploy the psc-controller chart into"
  type        = string
  default     = "default"
}

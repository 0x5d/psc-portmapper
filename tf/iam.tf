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
    "compute.firewalls.create",
    "compute.firewalls.delete",
    "compute.firewalls.get",
    "compute.firewalls.list",
    "compute.firewalls.update",
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
    "compute.instances.use",
    "compute.networks.getRegionEffectiveFirewalls",
    "compute.networks.setFirewallPolicy",
    "compute.networks.updatePolicy",
    "compute.networks.use",
    "compute.regionBackendServices.create",
    "compute.regionBackendServices.delete",
    "compute.regionBackendServices.get",
    "compute.regionBackendServices.list",
    "compute.regionBackendServices.update",
    "compute.regionBackendServices.use",
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
    "compute.subnetworks.use",
  ]
}

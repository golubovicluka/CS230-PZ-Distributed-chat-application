output "kube_context" {
  description = "kubectl context of the cluster"
  value       = "kind-${kind_cluster.this.name}"
}

output "registry" {
  description = "Push address of the local zot registry"
  value       = "localhost:${local.registry_port}"
}

output "urls" {
  description = "Browser entry points once the platform is synced"
  value = {
    chat     = "https://chat.localhost${var.https_port == 443 ? "" : ":${var.https_port}"}"
    argocd   = "https://argocd.chat.localhost${var.https_port == 443 ? "" : ":${var.https_port}"}"
    headlamp = "https://headlamp.chat.localhost${var.https_port == 443 ? "" : ":${var.https_port}"}"
    openbao  = "https://bao.chat.localhost${var.https_port == 443 ? "" : ":${var.https_port}"}"
  }
}

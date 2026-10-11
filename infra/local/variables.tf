variable "cluster_name" {
  description = "kind cluster name; the kubectl context is kind-<name>"
  type        = string
  default     = "chat"
}

variable "node_image" {
  description = "kind node image, pinned by digest"
  type        = string
  default     = "kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed"
}

variable "workers" {
  description = "Number of worker nodes"
  type        = number
  default     = 2

  validation {
    condition     = var.workers >= 1 && var.workers <= 4
    error_message = "workers must be between 1 and 4."
  }
}

variable "https_port" {
  description = "Host port on 127.0.0.1 that reaches the Envoy gateway; use 8443 if 443 is taken"
  type        = number
  default     = 443
}

variable "registry_image" {
  description = "zot image for the local registry, pinned by digest"
  type        = string
  default     = "ghcr.io/project-zot/zot-minimal:v2.1.22@sha256:6704abd9c4c988398d77915549d79c5daa2a2c5731964833e31ca039c9ac4ed3"
}

variable "repo_url" {
  description = "Git repository Argo CD syncs the platform from"
  type        = string
  default     = "https://github.com/golubovicluka/CS230-PZ-Distributed-chat-application.git"
}

variable "git_revision" {
  description = "Branch, tag or commit of repo_url that Argo CD deploys; it must be pushed"
  type        = string
  default     = "main"
}

variable "ca_cert_path" {
  description = "PEM certificate of the local development CA (scripts/local-ca.sh)"
  type        = string
}

variable "ca_key_path" {
  description = "PEM private key of the local development CA (scripts/local-ca.sh)"
  type        = string
  sensitive   = true
}

variable "argocd_chart_version" {
  description = "argo-cd Helm chart version"
  type        = string
  default     = "10.10.2"
}

variable "argocd_apps_chart_version" {
  description = "argocd-apps Helm chart version"
  type        = string
  default     = "2.0.6"
}

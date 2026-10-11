locals {
  registry_name = "kind-registry"
  registry_port = 5001
  certs_d       = abspath("${path.module}/containerd/certs.d")

  kubernetes = {
    host                   = kind_cluster.this.endpoint
    client_certificate     = kind_cluster.this.client_certificate
    client_key             = kind_cluster.this.client_key
    cluster_ca_certificate = kind_cluster.this.cluster_ca_certificate
  }

  application_health_lua = <<-LUA
    hs = {}
    hs.status = "Progressing"
    hs.message = ""
    if obj.status ~= nil and obj.status.health ~= nil then
      hs.status = obj.status.health.status
      if obj.status.health.message ~= nil then
        hs.message = obj.status.health.message
      end
    end
    return hs
  LUA
}

provider "kind" {}

provider "docker" {}

provider "helm" {
  kubernetes = local.kubernetes
}

provider "kubernetes" {
  host                   = local.kubernetes.host
  client_certificate     = local.kubernetes.client_certificate
  client_key             = local.kubernetes.client_key
  cluster_ca_certificate = local.kubernetes.cluster_ca_certificate
}

resource "kind_cluster" "this" {
  name           = var.cluster_name
  node_image     = var.node_image
  wait_for_ready = true

  kind_config {
    kind        = "Cluster"
    api_version = "kind.x-k8s.io/v1alpha4"

    containerd_config_patches = [
      <<-TOML
      [plugins."io.containerd.grpc.v1.cri".registry]
        config_path = "/etc/containerd/certs.d"
      TOML
    ]

    node {
      role = "control-plane"

      extra_port_mappings {
        container_port = 30443
        host_port      = var.https_port
        listen_address = "127.0.0.1"
        protocol       = "TCP"
      }

      extra_mounts {
        host_path      = local.certs_d
        container_path = "/etc/containerd/certs.d"
        read_only      = true
      }
    }

    dynamic "node" {
      for_each = range(var.workers)

      content {
        role = "worker"

        extra_mounts {
          host_path      = local.certs_d
          container_path = "/etc/containerd/certs.d"
          read_only      = true
        }
      }
    }
  }
}

resource "docker_image" "registry" {
  name         = var.registry_image
  keep_locally = true
}

resource "docker_volume" "registry" {
  name = "${var.cluster_name}-registry"
}

resource "docker_container" "registry" {
  name    = local.registry_name
  image   = docker_image.registry.image_id
  restart = "unless-stopped"

  ports {
    internal = 5000
    external = local.registry_port
    ip       = "127.0.0.1"
  }

  networks_advanced {
    name = "kind"
  }

  volumes {
    volume_name    = docker_volume.registry.name
    container_path = "/var/lib/registry"
  }

  depends_on = [kind_cluster.this]
}

resource "kubernetes_config_map_v1" "local_registry_hosting" {
  metadata {
    name      = "local-registry-hosting"
    namespace = "kube-public"
  }

  data = {
    "localRegistryHosting.v1" = yamlencode({
      host = "localhost:${local.registry_port}"
      help = "https://kind.sigs.k8s.io/docs/user/local-registry/"
    })
  }
}

resource "kubernetes_namespace_v1" "cert_manager" {
  metadata {
    name = "cert-manager"
  }
}

resource "kubernetes_secret_v1" "root_ca" {
  metadata {
    name      = "chat-root-ca"
    namespace = kubernetes_namespace_v1.cert_manager.metadata[0].name
  }

  type = "kubernetes.io/tls"

  data = {
    "tls.crt" = file(var.ca_cert_path)
    "tls.key" = file(var.ca_key_path)
    "ca.crt"  = file(var.ca_cert_path)
  }
}

resource "helm_release" "argocd" {
  name             = "argocd"
  namespace        = "argocd"
  create_namespace = true
  repository       = "https://argoproj.github.io/argo-helm"
  chart            = "argo-cd"
  version          = var.argocd_chart_version
  wait             = true
  timeout          = 600

  values = [yamlencode({
    global = {
      domain = "argocd.chat.localhost"
    }
    configs = {
      params = {
        "server.insecure"             = true
        "controller.diff.server.side" = true
      }
      cm = {
        "resource.customizations.health.argoproj.io_Application" = local.application_health_lua
      }
      repositories = {
        envoy-gateway = {
          name      = "envoy-gateway"
          type      = "helm"
          url       = "docker.io/envoyproxy"
          enableOCI = "true"
        }
      }
    }
    dex = {
      enabled = false
    }
    notifications = {
      enabled = false
    }
  })]
}

resource "helm_release" "platform" {
  name       = "platform-root"
  namespace  = helm_release.argocd.namespace
  repository = "https://argoproj.github.io/argo-helm"
  chart      = "argocd-apps"
  version    = var.argocd_apps_chart_version

  values = [yamlencode({
    applications = {
      platform = {
        namespace = helm_release.argocd.namespace
        project   = "default"
        source = {
          repoURL        = var.repo_url
          targetRevision = var.git_revision
          path           = "deploy/argocd/platform"
          helm = {
            valuesObject = {
              repoURL  = var.repo_url
              revision = var.git_revision
            }
          }
        }
        destination = {
          server    = "https://kubernetes.default.svc"
          namespace = helm_release.argocd.namespace
        }
        syncPolicy = {
          automated = {
            prune    = true
            selfHeal = true
          }
        }
      }
    }
  })]

  depends_on = [
    kubernetes_secret_v1.root_ca,
    kubernetes_config_map_v1.local_registry_hosting,
    docker_container.registry,
  ]
}

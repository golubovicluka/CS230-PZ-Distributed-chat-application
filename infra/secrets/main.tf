provider "vault" {
  address          = var.address
  token            = var.token
  skip_child_token = true
}

resource "vault_mount" "chat" {
  path        = "chat"
  type        = "kv"
  description = "Chat application secrets"
  options = {
    version = "2"
  }
}

resource "random_password" "redis" {
  length  = 32
  special = false
}

resource "vault_kv_secret_v2" "redis" {
  mount = vault_mount.chat.path
  name  = "redis"
  data_json = jsonencode({
    password = random_password.redis.result
  })
}

resource "vault_policy" "external_secrets" {
  name   = "external-secrets"
  policy = <<-HCL
    path "${vault_mount.chat.path}/data/*" {
      capabilities = ["read"]
    }

    path "${vault_mount.chat.path}/metadata/*" {
      capabilities = ["read", "list"]
    }
  HCL
}

resource "vault_auth_backend" "kubernetes" {
  type = "kubernetes"
}

resource "vault_kubernetes_auth_backend_config" "in_cluster" {
  backend         = vault_auth_backend.kubernetes.path
  kubernetes_host = "https://kubernetes.default.svc"
}

resource "vault_kubernetes_auth_backend_role" "external_secrets" {
  backend                          = vault_auth_backend.kubernetes.path
  role_name                        = "eso"
  bound_service_account_names      = ["external-secrets"]
  bound_service_account_namespaces = ["external-secrets"]
  token_policies                   = [vault_policy.external_secrets.name]
  token_ttl                        = 3600

  depends_on = [vault_kubernetes_auth_backend_config.in_cluster]
}

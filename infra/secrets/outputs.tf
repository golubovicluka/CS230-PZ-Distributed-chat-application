output "redis_secret_path" {
  description = "OpenBao path the chat chart's ExternalSecret reads"
  value       = "${vault_mount.chat.path}/${vault_kv_secret_v2.redis.name}"
}

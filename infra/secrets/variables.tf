variable "address" {
  description = "OpenBao address; just secrets port-forwards the in-cluster service here"
  type        = string
  default     = "http://127.0.0.1:18200"
}

variable "token" {
  description = "OpenBao token; the dev-mode root token by default"
  type        = string
  default     = "root"
  sensitive   = true
}

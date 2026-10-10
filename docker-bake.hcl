variable "IMAGE_REGISTRY" {
  default = "ghcr.io/golubovicluka"
}

variable "IMAGE_TAG" {
  default = "dev"
}

variable "PLATFORMS" {
  default = "linux/amd64,linux/arm64"
}

group "default" {
  targets = ["chatserver", "loadbalancer", "web"]
}

target "_common" {
  platforms = split(",", PLATFORMS)
  labels = {
    "org.opencontainers.image.source" = "https://github.com/golubovicluka/CS230-PZ-Distributed-chat-application"
  }
}

target "chatserver" {
  inherits   = ["_common"]
  context    = "."
  dockerfile = "build/Dockerfile"
  target     = "chatserver"
  tags       = ["${IMAGE_REGISTRY}/chat-chatserver:${IMAGE_TAG}"]
}

target "loadbalancer" {
  inherits   = ["_common"]
  context    = "."
  dockerfile = "build/Dockerfile"
  target     = "loadbalancer"
  tags       = ["${IMAGE_REGISTRY}/chat-loadbalancer:${IMAGE_TAG}"]
}

target "web" {
  inherits = ["_common"]
  context  = "chat-app"
  tags     = ["${IMAGE_REGISTRY}/chat-web:${IMAGE_TAG}"]
}

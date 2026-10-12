terraform {
  required_version = ">= 1.13.0"

  required_providers {
    vault = {
      source  = "hashicorp/vault"
      version = "5.12.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "3.9.1"
    }
  }
}

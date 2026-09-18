terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {}

resource "gregale_app" "api" {
  slug = "orders-api"
}

variable "git_ref" {
  type    = string
  default = "main"
}

resource "gregale_deployment" "api" {
  app_slug    = gregale_app.api.slug
  repo        = "acme/orders-api"
  ref         = var.git_ref
  environment = "production"
}

output "deployment_id" {
  value = gregale_deployment.api.deployment_id
}

output "preview_url" {
  value = gregale_deployment.api.preview_url
}

// For prebuilt OCI deployments, replace repo/ref with:
// image = "ghcr.io/acme/orders-api@sha256:..."

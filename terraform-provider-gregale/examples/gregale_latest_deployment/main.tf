terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {}

data "gregale_latest_deployment" "api" {
  app_slug = "orders-api"
}

output "deployment_id" {
  value = data.gregale_latest_deployment.api.deployment_id
}

output "deployment_status" {
  value = data.gregale_latest_deployment.api.status
}

output "preview_url" {
  value = data.gregale_latest_deployment.api.preview_url
}

terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {}

variable "deployment_id" {
  type = string
}

data "gregale_deployment" "release" {
  deployment_id = var.deployment_id
}

output "deployment_status" {
  value = data.gregale_deployment.release.status
}

output "preview_url" {
  value = data.gregale_deployment.release.preview_url
}

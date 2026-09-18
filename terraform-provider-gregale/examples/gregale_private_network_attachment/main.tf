terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {}

resource "gregale_private_network_attachment" "api" {
  app_slug   = "orders-api"
  network_id = "prod-vpc"
  region     = "fra1"
  cidrs      = ["10.30.0.0/16"]
}

output "status" {
  value = gregale_private_network_attachment.api.status
}

output "private_address" {
  value = gregale_private_network_attachment.api.address
}

terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {}

data "gregale_private_network" "shared" {
  network_id = var.network_id
}

resource "gregale_private_network_attachment" "api" {
  app_slug   = "orders-api"
  network_id = data.gregale_private_network.shared.id
  region     = data.gregale_private_network.shared.region
  cidrs      = [data.gregale_private_network.shared.cidr]
}

variable "network_id" {
  type        = string
  description = "Existing Gregale private network ID."
}

output "network_status" {
  value = data.gregale_private_network.shared.status
}

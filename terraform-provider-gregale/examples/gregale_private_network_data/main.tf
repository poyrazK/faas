terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {}

variable "private_network_id" {
  type        = string
  description = "Stable Gregale private network identifier to read."
}

data "gregale_private_network" "prod" {
  network_id = var.private_network_id
}

output "private_network_cidr" {
  value = data.gregale_private_network.prod.cidr
}

output "private_network_status" {
  value = data.gregale_private_network.prod.status
}

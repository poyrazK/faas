terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {}

resource "gregale_private_network" "app" {
  name   = "app"
  region = "fra1"
  cidr   = "10.20.0.0/16"
}

resource "gregale_private_network" "data" {
  name   = "data"
  region = "fra1"
  cidr   = "10.21.0.0/16"
}

resource "gregale_private_network_peering" "app_data" {
  network_id      = gregale_private_network.app.id
  peer_network_id = gregale_private_network.data.id
}

output "peering_status" {
  value = gregale_private_network_peering.app_data.status
}

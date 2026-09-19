terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {}

resource "gregale_private_network" "prod" {
  name          = "production"
  region        = "fra1"
  cidr          = "10.20.0.0/16"
  allowed_cidrs = ["10.20.0.0/24"]
}

output "network_id" {
  value = gregale_private_network.prod.id
}

output "network_status" {
  value = gregale_private_network.prod.status
}

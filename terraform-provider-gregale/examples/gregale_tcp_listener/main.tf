terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {}

resource "gregale_tcp_listener" "postgres" {
  app_slug   = "orders-api"
  name       = "postgres"
  guest_port = 5432
}

output "public_port" {
  value = gregale_tcp_listener.postgres.public_port
}

output "enabled" {
  value = gregale_tcp_listener.postgres.enabled
}

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

resource "gregale_domain" "api" {
  domain = "api.example.com"
  app_id = gregale_app.api.app_id
}

output "verification_status" {
  value = gregale_domain.api.verification_status
}

terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {}

data "gregale_app" "api" {
  slug = "orders-api"
}

resource "gregale_domain" "api" {
  domain = "api.example.com"
  app_id = data.gregale_app.api.id
}

output "app_url" {
  value = data.gregale_app.api.url
}

terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {}

resource "gregale_app" "api" {
  slug             = "orders-api"
  resource_profile = "small"
  max_concurrency  = 8
  health_path      = "/healthz"
}

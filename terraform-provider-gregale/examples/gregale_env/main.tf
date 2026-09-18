terraform {
  required_version = ">= 1.11.0"

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

resource "gregale_env" "log_level" {
  app_slug = gregale_app.api.slug
  key      = "LOG_LEVEL"
  value    = "info"
  scope    = "production"
}

output "env_updated_at" {
  value = gregale_env.log_level.updated_at
}

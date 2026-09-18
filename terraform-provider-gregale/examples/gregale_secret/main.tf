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

resource "gregale_secret" "database_url" {
  app_slug = gregale_app.api.slug
  key      = "DATABASE_URL"
  value    = var.database_url
  scope    = "production"
}

variable "database_url" {
  type      = string
  sensitive = true
}

output "secret_updated_at" {
  value = gregale_secret.database_url.updated_at
}

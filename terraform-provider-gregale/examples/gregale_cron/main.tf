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

resource "gregale_cron" "sync" {
  app_id          = gregale_app.api.app_id
  schedule        = "*/15 * * * *"
  path            = "/internal/sync"
  timezone        = "UTC"
  skip_if_running = true
}

output "last_fired_at" {
  value = gregale_cron.sync.last_fired_at
}

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

resource "gregale_alert" "latency" {
  app_slug       = gregale_app.api.slug
  name           = "p95-latency"
  metric         = "latency_p95_ms"
  comparison     = "gt"
  threshold      = 500
  window_spec    = "5m"
  webhook_url    = var.alert_webhook_url
  webhook_secret = var.alert_webhook_secret
}

variable "alert_webhook_url" {
  type = string
}

variable "alert_webhook_secret" {
  type      = string
  sensitive = true
}

output "alert_state" {
  value = gregale_alert.latency.state
}

# Gregale Terraform provider

This directory contains the first provider contract for Gregale. It is a
separate Go module so the provider can evolve and be released independently of
the control plane.

The initial surface is intentionally small:

- `gregale_app` manages an API app through the public app lifecycle API.
- `gregale_domain` manages a custom hostname binding and exposes DNS/TLS state.
- `gregale_alert` manages an app alert rule and exposes evaluation state.
- `gregale_cron` manages a scheduled app invocation and exposes scheduler state.
- `gregale_project_environment` reads a durable project environment without
  copying secrets into Terraform state.

Environment creation and deletion are not exposed as a Terraform resource yet:
the Gregale API currently has no safe delete operation for environments. This
keeps Terraform destroy from silently leaving unmanaged remote state.

## Provider configuration

```hcl
terraform {
  required_providers {
    gregale = {
      source = "gregale/gregale"
    }
  }
}

provider "gregale" {
  # Prefer GREGALE_TOKEN in CI. The value is never stored in resource state.
  token    = var.gregale_token
  base_url = "https://api.gregale.dev"
}
```

Alert resources use Terraform's write-only attribute support for webhook
secrets and therefore require Terraform 1.11 or later.

## App example

```hcl
resource "gregale_app" "api" {
  slug             = "orders-api"
  resource_profile = "small"
  max_concurrency  = 8
  idle_timeout_s   = 60
  health_path      = "/healthz"
}
```

## Custom domain

```hcl
resource "gregale_domain" "api" {
  domain = "api.example.com"
  app_id = gregale_app.api.app_id
}

output "domain_txt_record" {
  value     = gregale_domain.api.txt_record
  sensitive = true
}
```

## Alert rule

```hcl
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
```

The webhook secret is sent on create/update but is never written to the plan
or state. Gregale returns only `webhook_secret_masked`; Terraform refreshes
the rule's `state`, firing timestamps, and delivery configuration.

## Scheduled invocation

```hcl
resource "gregale_cron" "sync" {
  app_id         = gregale_app.api.app_id
  schedule       = "*/15 * * * *"
  path           = "/internal/sync"
  timezone       = "UTC"
  skip_if_running = true
}
```

The schedule uses the standard five-field cron format. Gregale evaluates it
in the configured IANA timezone and reports `last_fired_at` and any
`suspended_reason` during refresh.

Creating the resource returns the DNS challenge immediately. Gregale verifies
DNS and provisions TLS asynchronously; refresh the resource to observe
`verification_status`, `cert_status`, and certificate expiry without making
`terraform apply` wait for DNS propagation.

## Environment lookup

```hcl
data "gregale_project_environment" "production" {
  project_slug = "orders"
  slug         = "production"
}

output "production_environment_id" {
  value = data.gregale_project_environment.production.project_id
}
```

The provider uses the same public REST contract as the CLI and sends
idempotency keys for app, domain, alert, and cron creation. Application secret
values, alert webhook secrets, and bearer tokens are not returned by the
managed resources or recorded in state.

# Gregale Terraform provider

This directory contains the first provider contract for Gregale. It is a
separate Go module so the provider can evolve and be released independently of
the control plane.

The initial surface is intentionally small:

- `gregale_app` manages an API app through the public app lifecycle API.
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

The provider uses the same public REST contract as the CLI and sends an
idempotency key for app creation. Secret values and bearer tokens are not
returned by the managed resources or recorded in state.

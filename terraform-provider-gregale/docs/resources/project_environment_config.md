page_title: "gregale_project_environment_config Resource - Gregale"
subcategory: ""
description: |-
  Manages non-secret configuration for an existing Gregale project environment.
---

# gregale_project_environment_config (Resource)

Manages versioned, non-secret JSON configuration for an existing Gregale
project environment. The environment itself is not created or deleted by this
resource. Use `jsonencode(...)` for stable Terraform plans; Gregale validates
and canonicalizes the object before storing it.

Gregale rejects secret-shaped configuration keys. Use `gregale_secret` for
credentials and other sensitive values.

Destroying this resource replaces the environment configuration with `{}`. It
does not delete the project environment.

## Example Usage

```terraform
resource "gregale_project_environment_config" "production" {
  project_slug = "orders"
  environment  = "production"
  values = jsonencode({
    region   = "eu"
    replicas = 2
  })
}

output "config_hash" {
  value = gregale_project_environment_config.production.config_hash
}
```

## Schema

### Required

- `project_slug` (String) Project slug owning the environment.
- `environment` (String) Registered project environment slug.
- `values` (String) Non-secret JSON object. Prefer `jsonencode(...)` for stable plans.

### Read-only

- `id` (String) Composite `project_slug/environment` identifier.
- `version` (Number) Immutable Gregale environment configuration version.
- `config_hash` (String) SHA-256 hash of the canonical configuration object.
- `updated_at` (String) Timestamp when this configuration version was created.

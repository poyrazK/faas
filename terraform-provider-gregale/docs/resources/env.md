page_title: "gregale_env Resource - Gregale"
subcategory: ""
description: |-
  Manages a scoped Gregale app environment variable without storing its value in Terraform state.
---

# gregale_env (Resource)

Manages a runtime environment variable for a Gregale app. The value is sent
to Gregale's write endpoint but never returned by the API or stored in a
Terraform plan or state artifact.

Use `gregale_secret` for credentials and other sensitive values. The env
resource is intended for non-sensitive runtime configuration such as feature
flags and log levels.

The write-only value requires Terraform 1.11 or later.

## Example Usage

```terraform
resource "gregale_env" "log_level" {
  app_slug = "orders-api"
  key      = "LOG_LEVEL"
  value    = "info"
  scope    = "production"
}
```

## Schema

### Required

- `app_slug` (String) Slug of the Gregale app. Changing it forces replacement.
- `key` (String) Environment variable key, such as `LOG_LEVEL` or `FEATURE_FLAG`. Changing it forces replacement.
- `value` (String, Write-only) Environment variable value. Requires Terraform 1.11 or later and is never stored in plan or state.

### Optional

- `scope` (String) Environment scope. Defaults to `default`; changing it forces replacement.

### Read-only

- `created_at` (String) Creation timestamp.
- `updated_at` (String) Last update timestamp.

## Import

Default-scope variables use:

```shell
terraform import gregale_env.log_level orders-api/LOG_LEVEL
```

Scoped variables use:

```shell
terraform import gregale_env.log_level orders-api/production/LOG_LEVEL
```

After import, configure `value`. Gregale cannot return the original value.

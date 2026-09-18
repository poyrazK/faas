page_title: "gregale_secret Resource - Gregale"
subcategory: ""
description: |-
  Manages a scoped Gregale app secret without storing plaintext in Terraform state.
---

# gregale_secret (Resource)

Manages a secret for a Gregale app. The secret value is sent to Gregale's
write endpoint but never returned by the API or stored in a Terraform plan or
state artifact.

The write-only value requires Terraform 1.11 or later.

## Example Usage

```terraform
resource "gregale_secret" "database_url" {
  app_slug = "orders-api"
  key      = "DATABASE_URL"
  value    = var.database_url
  scope    = "production"
}
```

## Schema

### Required

- `app_slug` (String) Slug of the Gregale app. Changing it forces replacement.
- `key` (String) Secret key, such as `DATABASE_URL` or `API_TOKEN`. Changing it forces replacement.
- `value` (String, Write-only) Secret plaintext. Requires Terraform 1.11 or later and is never stored in plan or state.

### Optional

- `scope` (String) Environment scope. Defaults to `default`; changing it forces replacement.

### Read-only

- `created_at` (String) Creation timestamp.
- `kid` (String) Host key identity that sealed the current value.
- `updated_at` (String) Last update timestamp.
- `value_hash` (String, Sensitive) Opaque fingerprint used for value-equality diagnostics.

## Import

Default-scope secrets use:

```shell
terraform import gregale_secret.database_url orders-api/DATABASE_URL
```

Scoped secrets use:

```shell
terraform import gregale_secret.database_url orders-api/production/DATABASE_URL
```

After import, configure `value`. Gregale cannot return the original secret
plaintext.

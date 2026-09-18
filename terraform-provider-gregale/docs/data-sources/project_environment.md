---
page_title: "gregale_project_environment Data Source - Gregale"
subcategory: ""
description: |-
  Reads a durable Gregale project environment.
---

# gregale_project_environment (Data Source)

Reads a project environment and its promotion protection state. This data
source deliberately exposes metadata only; environment configuration and
secret values are not returned.

## Example Usage

```terraform
data "gregale_project_environment" "production" {
  project_slug = "orders"
  slug         = "production"
}
```

## Schema

### Required

- `project_slug` (String) Project slug.
- `slug` (String) Environment slug.

### Read-only

- `id` (String) Composite `project_slug/environment` identifier.
- `created_at` (String) Creation timestamp.
- `project_id` (String) Gregale project identifier.
- `protected` (Boolean) Whether the environment is protected.
- `updated_at` (String) Last-update timestamp.

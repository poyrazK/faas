---
page_title: "gregale_project_environment Resource - Gregale"
subcategory: ""
description: |-
  Manages a Gregale project environment with guarded deletion.
---

# gregale_project_environment (Resource)

Manages a durable project environment. `production` is created automatically
by Gregale and cannot be deleted. Protected environments and environments with
live releases also reject deletion.

## Example Usage

```terraform
resource "gregale_project_environment" "preview" {
  project_slug = "orders"
  slug         = "preview"
  protected    = false
}
```

## Import

Import with `<project-slug>/<environment-slug>`:

```shell
terraform import gregale_project_environment.preview orders/preview
```

## Schema

### Required

- `project_slug` (String) Project slug owning the environment.
- `slug` (String) Lowercase project environment slug.

### Optional

- `protected` (Boolean) Whether the environment is protected from destructive lifecycle operations and promotion.

### Read-Only

- `created_at` (String) Environment creation timestamp.
- `id` (String) Stable Gregale project environment identifier.
- `project_id` (String) Gregale project identifier.
- `updated_at` (String) Environment last-update timestamp.

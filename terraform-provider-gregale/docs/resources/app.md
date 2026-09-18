---
page_title: "gregale_app Resource - Gregale"
subcategory: ""
description: |-
  Manages a Gregale API application.
---

# gregale_app (Resource)

Manages a Gregale API application. The app slug is stable and changing it
forces replacement. Runtime hints are also replacement-only in this first
provider contract; deploy source and release policy remain separate workflows.

## Example Usage

```terraform
resource "gregale_app" "api" {
  slug             = "orders-api"
  resource_profile = "small"
  max_concurrency  = 8
  health_path      = "/healthz"
}
```

## Schema

### Required

- `slug` (String) Stable app slug.

### Optional

- `health_path` (String) Application health path.
- `health_path_wakes` (Boolean) Whether health requests may wake a parked app.
- `idle_timeout_s` (Number) Idle timeout in seconds.
- `max_concurrency` (Number) Maximum concurrent requests per instance.
- `ram_mb` (Number) Requested memory in MiB.
- `resource_profile` (String) Gregale resource profile.
- `runtime` (String) Runtime hint for the first deployment. Changing it forces replacement.
- `visibility` (String) App visibility.

### Read-only

- `app_id` (String) Gregale's immutable app identifier.
- `status` (String) Current app status.
- `url` (String) Current public app URL.

## Import

Apps are imported by slug:

```shell
terraform import gregale_app.api orders-api
```

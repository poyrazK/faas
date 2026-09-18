page_title: "gregale_app Data Source - Gregale"
subcategory: ""
description: |-
  Reads an existing Gregale API application without managing its lifecycle.
---

# gregale_app (Data Source)

Reads an existing Gregale API application without creating, updating, or
deleting it. Use this data source to adopt an app created by the CLI, dashboard,
or another Terraform stack and compose its immutable ID with other resources.

## Example Usage

```terraform
data "gregale_app" "api" {
  slug = "orders-api"
}

resource "gregale_domain" "api" {
  domain = "api.example.com"
  app_id = data.gregale_app.api.id
}

output "app_url" {
  value = data.gregale_app.api.url
}
```

## Schema

### Required

- `slug` (String) Stable app slug to look up.

### Read-only

- `id` (String) Gregale's immutable app identifier.
- `health_path` (String) Application health path used by readiness checks.
- `health_path_wakes` (Boolean) Whether requests to the health path may wake a parked app.
- `idle_timeout_s` (Number) Idle timeout in seconds before the app can park.
- `max_concurrency` (Number) Maximum concurrent requests per instance.
- `ram_mb` (Number) Requested memory in MiB.
- `resource_profile` (String) Gregale resource profile.
- `runtime` (String) Runtime hint recorded for the app.
- `status` (String) Current app status.
- `type` (String) Gregale workload type.
- `url` (String) Current public app URL, when one is available.
- `visibility` (String) Current app visibility.

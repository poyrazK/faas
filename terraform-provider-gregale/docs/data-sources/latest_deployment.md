---
page_title: "gregale_latest_deployment Data Source - Gregale"
subcategory: ""
description: |-
  Reads the newest deployment for an existing Gregale app without managing its lifecycle.
---

# gregale_latest_deployment (Data Source)

Reads the newest deployment for an existing Gregale app. Gregale resolves the
deployment by `created_at` descending, so this data source is useful for CI,
release dashboards, and downstream configuration that should follow the
current app deployment without passing an opaque deployment ID.

## Example Usage

```terraform
data "gregale_latest_deployment" "api" {
  app_slug = "orders-api"
}

output "deployment_status" {
  value = data.gregale_latest_deployment.api.status
}

output "preview_url" {
  value = data.gregale_latest_deployment.api.preview_url
}
```

## Schema

### Required

- `app_slug` (String) Stable Gregale app slug whose newest deployment should be read.

### Read-only

- `id` (String) Gregale's immutable deployment identifier.
- `app_id` (String) Stable Gregale app identifier.
- `deployment_id` (String) Immutable Gregale deployment identifier.
- `build_id` (String) Associated build identifier.
- `image_digest` (String) Produced image digest.
- `kind` (String) Deployment build kind.
- `status` (String) Deployment lifecycle status.
- `scope` (String) Runtime environment scope selected by the deployment.
- `commit_sha` (String) Resolved commit SHA.
- `source_url` (String) Resolved source URL.
- `created_at` (String) Deployment creation timestamp.
- `preview_url` (String) Shareable preview URL, when available.
- `preview_host` (String) Preview hostname, when available.
- `stage_state` (String) JSON deployment stage summary.
- `error` (String) Deployment failure message, when applicable.
- `error_code` (String) Structured deployment failure code, when applicable.
- `error_hint` (String) Failure hint, when applicable.
- `error_why` (String) Failure explanation, when applicable.
- `error_fix` (String) Failure remediation, when applicable.

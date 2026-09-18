---
page_title: "gregale_deployment Data Source - Gregale"
subcategory: ""
description: |-
  Reads an existing Gregale deployment without managing its lifecycle.
---

# gregale_deployment (Data Source)

Reads an existing Gregale deployment created by the CLI, dashboard, CI, or
another Terraform stack. Use it to compose deployment status and preview
metadata into downstream Terraform configuration without creating or owning a
deployment.

## Example Usage

```terraform
data "gregale_deployment" "release" {
  deployment_id = var.deployment_id
}

output "preview_url" {
  value = data.gregale_deployment.release.preview_url
}
```

## Schema

### Required

- `deployment_id` (String) Immutable Gregale deployment identifier to look up.

### Read-only

- `id` (String) Gregale's immutable deployment identifier.
- `app_id` (String) Stable Gregale app identifier.
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

page_title: "gregale_deployment Resource - Gregale"
subcategory: ""
description: |-
  Deploys a GitHub source ref to Gregale and exposes lifecycle and preview metadata.
---

# gregale_deployment (Resource)

Deploys a GitHub repository ref through Gregale's headless source-ref path.
Create waits for the deployment to reach `live` and reports structured failure
details when it does not.

The provider requires a GitHub repository installation that Gregale can use to
resolve the repository and fetch the source archive.

## Example Usage

```terraform
resource "gregale_deployment" "api" {
  app_slug    = "orders-api"
  repo        = "acme/orders-api"
  ref         = var.git_ref
  environment = "production"
}
```

## Schema

### Required

- `app_slug` (String) Slug of the Gregale app. Changing it forces replacement.
- `repo` (String) GitHub repository slug, for example `acme/orders-api`. Changing it forces replacement.
- `ref` (String) Git branch, tag, short commit SHA, or full commit SHA. Changing it forces replacement.

### Optional

- `environment` (String) Registered Gregale project environment to target. Changing it forces replacement.
- `no_triggers` (Boolean) Skip trigger declarations found in `gregale.yaml`. Changing it forces replacement.

### Read-only

- `deployment_id` (String) Immutable deployment identifier.
- `app_id` (String) Stable app identifier.
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

## Lifecycle behavior

The deployment inputs are immutable. Changing them creates a new deployment.
Destroy cancels only deployments in `pending`, `building`, `imaging`, or
`snapshotting`; it does not roll back or delete a live deployment.

## Import

Import uses the app slug and deployment ID:

```shell
terraform import gregale_deployment.api orders-api/deployment-uuid
```

After import, configure `repo` and `ref` so Terraform can describe the
immutable deployment inputs.

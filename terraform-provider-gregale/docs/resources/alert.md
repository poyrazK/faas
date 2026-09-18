page_title: "gregale_alert Resource - Gregale"
subcategory: ""
description: |-
  Manages a Gregale alert rule.
---

# gregale_alert (Resource)

Manages a durable alert rule for a Gregale app. Alert rules evaluate a metric
over a bounded window and deliver a signed webhook when the threshold fires.

Webhook secrets use Terraform's write-only attribute support and require
Terraform 1.11 or later. The provider never stores the secret in a plan or
state artifact.

## Example Usage

```terraform
resource "gregale_alert" "latency" {
  app_slug       = "orders-api"
  name           = "p95-latency"
  metric         = "latency_p95_ms"
  comparison     = "gt"
  threshold      = 500
  window_spec    = "5m"
  webhook_url    = var.alert_webhook_url
  webhook_secret = var.alert_webhook_secret
}
```

## Schema

### Required

- `app_slug` (String) Slug of the Gregale app. Changing it forces replacement.
- `comparison` (String) One of `gt`, `gte`, `lt`, or `lte`.
- `metric` (String) Metric to evaluate. Changing it forces replacement.
- `name` (String) Unique alert rule name within the app.
- `threshold` (Number) Metric threshold that causes the alert to fire.
- `webhook_secret` (String, Write-only) HMAC secret for alert deliveries. It is never stored in plan or state.
- `webhook_url` (String) HTTPS endpoint that receives alert deliveries.
- `window_spec` (String) Evaluation window, such as `5m`, `1h`, or `24h`.

### Optional

- `action` (String) Action on fire: `webhook`, `rollback`, `demote`, or `promote`.
- `cooldown_minutes` (Number) Minimum minutes between alert deliveries.
- `enabled` (Boolean) Whether Gregale evaluates this alert rule.
- `failure_source` (String) Source filter for `failed_invocations`. Changing it forces replacement.

### Read-only

- `alert_id` (String) Gregale's immutable alert rule identifier.
- `app_id` (String) Gregale's immutable app identifier.
- `created_at` (String) Creation timestamp.
- `last_evaluated_at` (String) Most recent evaluation timestamp.
- `last_fired_at` (String) Most recent firing timestamp.
- `state` (String) Current evaluation state, such as `ok` or `firing`.
- `updated_at` (String) Last update timestamp.
- `webhook_secret_masked` (String) Masked indicator for the configured webhook secret.

## Import

Alerts are imported by app slug and alert ID:

```shell
terraform import gregale_alert.latency orders-api/alert-uuid
```

After import, configure `webhook_secret` in the resource configuration. Gregale
cannot return the original secret.

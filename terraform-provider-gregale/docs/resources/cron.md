page_title: "gregale_cron Resource - Gregale"
subcategory: ""
description: |-
  Manages a Gregale scheduled app invocation.
---

# gregale_cron (Resource)

Manages a durable scheduled POST to a Gregale app. The scheduler uses a
standard five-field cron expression and an optional IANA timezone.

## Example Usage

```terraform
resource "gregale_cron" "sync" {
  app_id          = "app-uuid"
  schedule        = "*/15 * * * *"
  path            = "/internal/sync"
  timezone        = "UTC"
  schedule_policy = {
    overlap                = "skip"
    start_deadline_seconds = 120
    missed_runs            = "coalesce_latest"
  }
  failure_rules_json = jsonencode({
    version = 1
    rules = [
      { outcome_codes = ["upstream_unavailable"], action = "retry" },
      { outcome_codes = ["invalid_record"], action = "fail_partition" },
    ]
    unmatched_failure = "fail_partition"
    uncertain_outcome = "hold"
  })
}
```

## Schema

### Required

- `app_id` (String) Stable Gregale app identifier. Changing it forces replacement.
- `schedule` (String) Five-field cron expression in `m h dom mon dow` format.

### Optional

- `enabled` (Boolean) Whether the scheduler evaluates this cron.
- `failure_rules_json` (String) Optional versioned failure policy encoded as JSON. HTTP Crons match `outcome_codes` returned by the handler in `X-Gregale-Outcome-Code`; HTTP status and exit-code matchers are not supported. Retries use the account plan's finite durable invocation budget.
- `path` (String) App path to POST. Defaults to `/`.
- `skip_if_running` (Boolean) Skip a fire while the previous invocation is still running.
- `schedule_policy` (Object) Optional recurring-work policy. `overlap` accepts `allow`, `skip`, or `replace`; `start_deadline_seconds` sets the maximum delay before first start (zero disables it); `missed_runs` accepts `skip` or `coalesce_latest`. Replacement waits for an in-flight HTTP request to finish because Gregale cannot confirm that an already delivered request has stopped.
- `timezone` (String) IANA timezone used to interpret the schedule. Defaults to `UTC`.

### Read-only

- `created_at` (String) Creation timestamp.
- `cron_id` (String) Gregale's immutable cron identifier.
- `last_fired_at` (String) Most recent fire timestamp, when available.
- `suspended_reason` (String) Why Gregale suspended the schedule, when applicable.

## Import

Crons are imported by app ID and cron ID:

```shell
terraform import gregale_cron.sync app-uuid/cron-uuid
```

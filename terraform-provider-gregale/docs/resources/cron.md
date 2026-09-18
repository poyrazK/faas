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
  skip_if_running = true
}
```

## Schema

### Required

- `app_id` (String) Stable Gregale app identifier. Changing it forces replacement.
- `schedule` (String) Five-field cron expression in `m h dom mon dow` format.

### Optional

- `enabled` (Boolean) Whether the scheduler evaluates this cron.
- `path` (String) App path to POST. Defaults to `/`.
- `skip_if_running` (Boolean) Skip a fire while the previous invocation is still running.
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

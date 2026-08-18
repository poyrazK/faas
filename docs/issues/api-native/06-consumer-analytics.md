# Sub-issue #06 — Consumer-level usage analytics

Parent: [README.md](README.md)

## Problem

`usage_monthly` and `usage_daily` group only by `(account_id, app_id)`:

- `migrations/00067_extend_metering_telemetry.sql:88-105` — monthly rows.
- `migrations/00067_extend_metering_telemetry.sql:107-129` — daily rows.
- `/v1/usage` response at `pkg/api/dto.go:1820-1875` — no `consumer_id`
  field, just `app_id` aggregates.

Once sub-issue #05 introduces the consumer identity, the metering tables
and the usage DTO need to carry it.

## Proposal

### Schema

```sql
ALTER TABLE usage_monthly ADD COLUMN consumer_id UUID NULL REFERENCES consumer_keys(id) ON DELETE SET NULL;
ALTER TABLE usage_daily  ADD COLUMN consumer_id UUID NULL REFERENCES consumer_keys(id) ON DELETE SET NULL;
```

Existing rows get `consumer_id=NULL` (untagged traffic, e.g. anonymous
or platform-key traffic). New rows are written with the consumer id when
present.

The meterd pipeline (today keyed on `(account_id, app_id, period_start)`)
gets a second write path keyed on `(account_id, app_id, consumer_id,
period_start)`. The aggregator merges the two views into the same
counter family; the dashboard chooses which key to read by.

### DTO additions

`/v1/usage` gets a `breakdown_by=consumer` query parameter. Response:

```yaml
type: object
properties:
  total:
    $ref: '#/components/schemas/UsageAggregate'
  by_consumer:
    type: array
    items:
      type: object
      properties:
        consumer_id: { type: string, format: uuid }
        consumer_name: { type: string }   # resolved from consumer_keys.name
        requests: { type: integer }
        gb_seconds: { type: number }
        egress_bytes: { type: integer }
```

When `consumer_id IS NULL` (untagged), the row is rolled into a single
"anonymous" bucket so the totals still reconcile.

### Privacy

The aggregate never includes consumer secrets. Only the resolved
`name` is shown; PII is the customer's responsibility (they named the
key).

### Retention

Existing retention policies for `usage_monthly` / `usage_daily` apply
unchanged. Per-consumer rows are dropped automatically when the
underlying consumer_key is deleted (`ON DELETE SET NULL`).

### Limits (pkg/api/limits.go)

- `usage_breakdown_max_rows` = 1000 (cap on array length).

## Acceptance

1. After #05 lands, a customer issues two consumer keys. `/v1/usage` with
   `breakdown_by=consumer` returns two rows plus the `anonymous` row.
2. Revoking a consumer key sets the row to `anonymous` on next meter
   flush; the historical aggregate rows are preserved with `consumer_id=NULL`
   (audit trail).
3. The total of `by_consumer[].requests + anonymous.requests == total.requests`
   (reconciliation property test).
4. Migration is numbered in the next available slot; cross-PR fence check
   runs before commit.

## Dependencies

- #05 (consumer identity).
- Feeds #07 (route metrics gain a `consumer_id` reserved label) and
  #08 (`request_logs.consumer_id`).

## Audit provenance

- `migrations/00067_extend_metering_telemetry.sql:88-105`, `:107-129` —
  grouping by `(account_id, app_id)`.
- `pkg/api/dto.go:1820-1875` — `/v1/usage` DTO.
- ADR-104 — throttle-keying only, not metering.

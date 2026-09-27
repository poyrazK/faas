# ADR-076 · Outbound webhook delivery reliability (ledger + DLQ)

- **Status:** accepted
- **Date:** 2026-08-06
- **Issue:** #476
- **Supersedes:** none

## Decision

Ship seven atomic commits that close the outbound webhook delivery
reliability gap end-to-end: schema migration → `pkg/webhookout`
header-set seam → `pkg/state` CRUD + delivery ledger →
`pkg/webhook` dispatcher + schedd wiring → apid CRUD handlers +
OpenAPI → gregale CLI + SDK regen → e2e + ADR + STATUS.

## Why

Today every outbound webhook fires synchronously from meterd
(alert delivery, up to 5 attempts, no persistence) or directly from
apid on cron.fired (no retry at all). When a customer endpoint
500s, the event is lost. Issue #476 closes the gap with:

- A persistent delivery ledger (`app_webhook_deliveries`) so the
  retry state survives process restarts.
- A schedd-side dispatcher (`pkg/webhook.Dispatcher`) that drains
  the ledger on a 5s tick with a 32-row cap, retries with
  exponential backoff + jitter, and DLQs at attempt 7.
- A per-account fairness filter so one noisy customer can't starve
  the rest of the fleet.
- A customer-facing deliveries endpoint so the customer can see
  what's queued, in-flight, succeeded, failed, or dead — and retry
  a dead row manually.

This deliberately does NOT extend the alert-deliveries surface
(see §3.1).

## Decisions

### 3.1 Parallel outbound surface, not extending `alert_deliveries`

Alert delivery is alert-shaped (`alert_rule_id`, `observed_value`,
cool-down window); webhook delivery is event-shaped (`event`,
`payload`, no cool-down). Co-locating them would force a union
type on the ledger and complicate the dispatcher's claim query.
Mirrors the cron / alert split that already exists. The alert
delivery path is unchanged; the new surface lives at
`/v1/apps/{slug}/webhooks[/...]`.

### 3.2 Per-account fairness via SQL round-robin claim

The original design assumed `ORDER BY account_id, next_attempt_at`
would produce round-robin claims. It actually groups all due rows
from the first account before the next account, so a busy account
can fill the 32-row batch. Fair selection still needs a separate
implementation and a test that counts delivered rows by webhook.

### 3.3 DLQ at attempt 7 with three retry-policy presets

The nominal 7.5-hour retry window has 7 attempts with the default
schedule (`30s`, `2m`, `10m`, `20m`, `1h`, `6h`, exhausted). Three closed
presets:

- `default` — schedule above.
- `aggressive` — halves each interval (`15s`, `1m`, `5m`, `10m`, `30m`,
  `3h`, exhausted).
- `none` — DLQ on first 5xx (no retries).

`aggressive` and `none` are plan-gated to Hobby+ (Free never sees
the webhook surface per §3.4). The closed enum is enforced at
both the CLI (`--retry-policy default|aggressive|none`) and the
apid handler (`api.AllowedAppWebhookRetryPolicies`). A typo
surfaces as 400 `app_webhook_invalid` BEFORE the row is created.

### 3.4 Plan-tier gate: `WebhookPerApp` + `WebhookPerAccount`

Plan gating follows the cron / alert-rule precedent: a closed
table in `pkg/api/limits.go`. Free has 0 webhooks
(`WebhookPerApp = 0` → 402 `plan_webhooks_not_allowed`); Hobby 5 /
20, Pro 20 / 100, Scale 100 / 500. The quota gate runs inside the
same `CreateAppWebhookIfUnderQuota` transaction that the crons +
alert-rules surfaces use (apps-row FOR UPDATE lock + per-account
count under tx).

### 3.5 Clock injection via dispatcher struct fields

The dispatcher's `Sleeper` and `Now` are struct fields, not package
vars. This mirrors the `pkg/webhookdedupe.nowFunc` precedent but
is goroutine-safe under parallel tests because each dispatcher
instance owns its own clock. The 7.5-hour DLQ path is testable in
≤1s wall by setting `Sleeper = func(time.Duration) {}`.

### 3.6 Header names `X-Faas-Webhook-*`

The dispatcher's wire format uses `X-Faas-Webhook-Signature`,
`X-Faas-Webhook-Timestamp`, `X-Faas-Webhook-Attempt`, and
`X-Faas-Delivery-Id` (the last one is the delivery-row primary
key, stable across retries — verifier can dedupe by it). The
alert path keeps `X-Faas-Alert-*` headers unchanged. The split
keeps the alert wire stable while the webhook wire is brand-new
and unconsumed by anyone outside the platform.

### 3.7 Secret sealing via `secretbox.SealBytes` with namespace `"APP_WEBHOOK"`

The webhook secret is sealed at apid-write time using
`secretbox.SealBytes(recipient, "APP_WEBHOOK", plaintext, 256)`,
mirroring the alert-rule sealing pattern. The dispatcher unseals
with `secretbox.OpenBytesMulti` against the host age identity
loaded via `IdentityLoader`. The plaintext NEVER crosses the
wire after the create round-trip — the response shape carries
only `webhook_secret_sealed_masked: "***"`. Per the issue's "no
plaintext in logs" rule and CLAUDE.md §11, the plaintext is
destroyed at function exit (`_ = plaintext`).

### 3.8 Migration slot fence pattern

Slot 140 carries the `app_webhooks` table; slot 141 carries
`app_webhook_deliveries`. Slot 139 is a fence reservation
matching the `00130_reserve_slot.sql` StatementBegin/End shape,
in case any other PR is concurrently claiming 139 or 141. ADR-041
fence pattern; renumber past reservations at PR creation per
memory `migration-slot-renumber-at-pr-creation`.

## Consequences

Positive:

- Customers get persistent retries and a DLQ instead of dropped
  events on a 5xx — the headline win. `app_webhook_deliveries`
  survives schedd restart because the row is on disk before the
  dispatcher attempts delivery.
- Per-account fairness emerges from the claim query — no new
  state table, no config knob, no operator maintenance.
- The retry-policy closed set (default | aggressive | none) gives
  customers three load-bearing presets without the maintenance
  cost of a free-form retry-policy DSL.
- Sealed-secret + masked-response shape mirrors the alert-rule
  precedent, so a single host.age identity covers both surfaces.

Negative / costs:

- One row per delivery means the table grows linearly with the
  customer's webhook volume. The `succeeded` rows never get GC'd
  today; a future cron (issue #476 follow-up) should partition by
  month and detach partitions older than 90 days. The dashboard
  alert `snapshot_fleet_avg_mb` does NOT cover this table — a
  new `webhook_deliveries_table_mb` alert is queued for the
  follow-up.
- The 5-second tick + 32/tick cap is a deliberate batching
  trade-off: a single noisy account could push out the per-tick
  cap, but the ORDER BY round-robin + partial index keeps it
  bounded. A larger fleet might want a sub-tick cap per account,
  but at today's fleet size the contract is sufficient.
- The dispatcher is a schedd-only goroutine today; future
  multi-schedd deployments (ADR-064 cross-node rebalance) would
  need a per-node cap-aware partition to avoid a thundering herd
  on a single busy account. Out of scope for #476.
- Retry-policy='none' has no auto-retry; customers who flip it
  on by accident get a DLQ at first failure and must call
  `webhooks retry` to re-arm. The CLI surfaces the policy name
  on the `webhooks list` row so the customer can verify.
- Audit volume: the dispatcher emits one audit row per delivery
  attempt (`webhook.delivered` / `webhook.failed` / `webhook.dead`)
  on top of the customer's CRUD audit rows. A busy fleet will
  see ~32 audit rows/tick × 12 ticks/min = ~384 rows/min from the
  dispatcher alone. The audit table is sharded by created_at
  month and the dashboard's `audit_log_volume` panel surfaces a
  tripwire at 5k rows/min.

## Rejected alternatives

- **Extend `alert_deliveries` to carry outbound webhooks.** Rejected
  by §3.1: alert delivery is alert-shaped (`alert_rule_id`,
  `observed_value`, cool-down window); webhook delivery is
  event-shaped (`event`, `payload`, no cool-down). A union type
  on the ledger would break the dispatcher's claim query.
- **Token-bucket fairness (per-account state table).** Rejected:
  the SQL `ORDER BY account_id, next_attempt_at` round-robin is
  sufficient at the 32/tick cap. A token bucket adds a state
  table, a refresh tick, and a config knob for a benefit no
  current customer reads.
- **Free-form retry policy DSL.** Rejected: three closed presets
  cover 100% of observed customer use cases; a DSL would invite
  unbounded retry budgets and complicate the dispatcher's backoff
  shape. A new preset is a closed-set extension + ADR, not a
  parser.
- **Synchronous dispatch from apid (mirror meterd alerts).**
  Rejected: synchronous dispatch blocks the apid request thread,
  has no DLQ, and dies with the apid process. Persistent ledger +
  schedd dispatcher is the only shape that survives the customer's
  5xx.
- **Counter-based rate limiter (e.g. token bucket per webhook).**
  Rejected: same as token-bucket fairness above. The customer's
  endpoint is rate-limited by the customer's own ingress; our
  job is to retry, not police their receive rate.
- **Wire payload format change.** Rejected: reusing
  `pkg/webhookout.Signer`'s HMAC-SHA256 over `<unix>.<delivery_id>.<body>`
  keeps the customer-side verifier stable. The only header rename
  is `X-Faas-Alert-Id` → `X-Faas-Delivery-Id` (the new stable
  identifier is the delivery row id, not the alert rule id).
- **Single `app_webhooks_delivery` table (no separate ledger).**
  Rejected: subscriptions are small, infrequent writes; deliveries
  are high-volume, mutable state. A single table would either
  bloat the subscription scan or fight the partial-index claim
  query. Two tables, FK CASCADE.
- **Webhook destination stored as URL string + post-write
  SSRF probe.** Rejected: post-write probes race DNS-rebinding
  attacks; the URL must be re-validated on every dispatch
  (`pkg/oci/egress.go::resolveAndCheckEgress`) — the create-time
  check is a fast-fail gate, not a security boundary.

## Out of scope

- Per-webhook custom retry policies beyond the three presets.
- Cross-region webhook fan-out.
- Webhook destinations to customer-supplied endpoints (S3/SQS).
- Migrating the existing alert-delivery path to this shape.
- Payload signing scheme change (reuses `pkg/webhookout.Signer`).

## Verification

- `make migrations-check` — green; slot 140/141 fence at 139.
- `go test -race -count=1 ./cmd/apid/... ./pkg/api/...
  ./pkg/webhook/... ./pkg/webhookout/...` — green.
- `make sdk-gen` — Node SDK regenerated; twice-check passes
  (deterministic).
- `make spec-sync` — `pkg/apid/openapi.yaml` matches
  `api/openapi.yaml` after the webhook schema block is moved
  inside `components.schemas` (the initial placement inside
  `components.responses` broke the Node SDK regen — caught by
  `make sdk-gen`).
- `cmd/e2e/webhook_e2e_test.go` — green in-process end-to-end
  (apid handler → MemStore → dispatcher → httptest receiver).
- The cross-process wire tripwire (real Postgres + daemons) lives
  in the schedd-binary smoke test once `cmd/schedd` exposes a
  CLI flag for the dispatcher config; deferred to a follow-up.

## Audit emissions

- `app.webhook_created` — POST /v1/apps/{slug}/webhooks
- `app.webhook_updated` — PATCH (only on actual change)
- `app.webhook_deleted` — DELETE
- `app.webhook_secret_rotated` — POST /rotate-secret
- `app.webhook_delivery_retried` — POST /deliveries/{id}/retry
- `webhook.delivered` — dispatcher success (commit 4)
- `webhook.failed` — dispatcher retry (commit 4)
- `webhook.dead` — dispatcher DLQ (commit 4)

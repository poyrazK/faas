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

### 3.2 Per-account fairness via rotating SQL claim

The original `ORDER BY account_id, next_attempt_at` grouped a busy
account's rows and could fill the entire 32-row batch. The claim now
enumerates due accounts, reads a bounded number of oldest rows per
account through the partial index, and interleaves them before
`FOR UPDATE SKIP LOCKED`. The starting account rotates every five
seconds by one batch width, sharing extra slots when the batch size
is not divisible by the account count. The property test counts
delivered rows by webhook after each tick.

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

On a retryable `429` or `503`, a valid receiver `Retry-After` value
(delay in seconds or HTTP date) can postpone the next attempt beyond
the preset backoff. The later deadline wins. Receiver delays are capped
at 24 hours; invalid or past values fall back to the preset schedule.
This does not add attempts or override `retry_policy=none`. The chosen
next attempt time is recorded in delivery attempt history. Raw response
headers are not retained or logged.

The same receiver deadline also pauses new claims for that webhook
subscription across schedd instances. A fenced attempt completion extends
the subscription's persisted `receiver_cooldown_until` monotonically; an
older or stale attempt cannot shorten it. Other subscriptions continue to
drain, and attempts already claimed may finish. The deadline expires without
a sweep. Health responses and the dashboard show an active cooldown while
pending counts remain visible; intentionally paused rows do not count as
overdue until the deadline passes.
Changing a subscription's target URL clears its active cooldown; a late
response from the old URL cannot pause the replacement target.

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

### 3.9 Immutable attempt history

`app_webhook_delivery_attempts` records one completed outcome per
delivery attempt: retry round, attempt number, receiver status, error,
start/finish times, and next scheduled retry. The attempt insert and
the lease-fenced delivery update run in one SQL statement, so a stale
worker cannot create an outcome that did not take effect. Manual
retry, including the shared dead-letter replay path, increments the
delivery's `replay_generation` before attempt numbers restart at 1.
`GET /deliveries/{did}/attempts` and the dashboard expose bounded,
newest-first pages. Request and response bodies, headers, and signing
secrets are not retained. Existing delivery rows have no backfilled
attempt history; the ledger begins when this migration deploys.

### 3.10 Delivery health and fleet alerts

Each webhook exposes current `pending`, `in_flight`, and `dead` counts,
the oldest overdue due time, and terminal outcomes over the preceding
24 hours. The success rate is succeeded / (succeeded + dead) and is
omitted when no delivery completed in that window. The dashboard and
scoped app, account release, and platform tenant APIs share this
snapshot. A webhook-ID index bounds the per-subscription query; a
partial due-time index backs the fleet oldest-due poll.

Schedd polls the oldest due delivery once a minute. Fleet metrics
publish its age, newly dead deliveries, and poll success without
account or webhook labels. Alerts identify a queue older than 15
minutes, a spike of more than 20 dead deliveries in ten minutes,
and a failed poll. The poll-failure signal prevents a stale queue-age
gauge from appearing healthy.

### 3.11 Delivery retention

Schedd deletes up to 500 terminal (`succeeded` or `dead`) deliveries each
minute after 90 days since their last state update. A partial index on
`(updated_at, id)` keeps selection bounded; `FOR UPDATE SKIP LOCKED` avoids
waiting on concurrent replays or other schedulers. The attempt-history FK
cascades on deletion. The unified dead-letter projection, which duplicates
the payload, is removed in the same transaction if still present. The unified
projection has its own shorter retention window. Active `pending` and
`in_flight` rows are never pruned.
Replaying a dead delivery updates its timestamp, so a recent dead delivery
remains available for manual retry. Delivery and attempt storage bytes,
cleanup success, failures, and deleted row counts are fleet-wide metrics.
Alerts cover failed cleanup and storage above 5 GiB. Monthly partitioning
remains an option if batched deletion cannot keep up with fleet volume.

### 3.12 Dispatcher backpressure

The dispatcher has 64 process-wide delivery slots in addition to its
32-per-tick claim limit. It reserves slots before each database claim and
requests only the currently free capacity. Empty and failed claims release
unused reservations; every completed worker releases its slot. When all slots
are occupied, the next tick leaves deliveries pending in the durable ledger
instead of leasing more rows. Fleet metrics report running workers and whether
all slots are allocated. The existing overdue-queue alert detects sustained
backlogs while the saturation signal distinguishes capacity pressure from an
idle or failing claim loop.

## Consequences

Positive:

- Customers get persistent retries and a DLQ instead of dropped
  events on a 5xx — the headline win. `app_webhook_deliveries`
  survives schedd restart because the row is on disk before the
  dispatcher attempts delivery.
- Per-account fairness comes from the rotating claim query — no new
  state table, no config knob, no operator maintenance.
- The retry-policy closed set (default | aggressive | none) gives
  customers three load-bearing presets without the maintenance
  cost of a free-form retry-policy DSL.
- Sealed-secret + masked-response shape mirrors the alert-rule
  precedent, so a single host.age identity covers both surfaces.

Negative / costs:

- One row per delivery grows with customer webhook volume. Terminal
  rows are retained for 90 days and removed in bounded batches;
  active rows can still grow during an extended dispatch outage.
  The storage-size alert covers both delivery and attempt tables.
- The 5-second tick, 32/tick claim cap, and 64 in-flight slots are
  deliberate batching and capacity trade-offs. Fair selection
  enumerates due accounts on each tick; a much larger backlog may
  need a maintained queue-head index or account cursor to keep that
  scan cheap.
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
  event-shaped (`event`, `payload`, no alert-rule cool-down). A union type
  on the ledger would break the dispatcher's claim query.
- **Token-bucket fairness (per-account state table).** Rejected:
  the bounded rotating claim provides per-batch fairness without
  a state table, refresh tick, or config knob.
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

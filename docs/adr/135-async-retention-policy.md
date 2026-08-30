# ADR-135 · Async retention policy (per-plan ladder)

- **Status:** accepted
- **Date:** 2026-08-29
- **Issue / PR:** mega-PR (PR-A through PR-E as atomic commits) on
  branch `worktree-feat-dispatch-contract-pr-a`
- **Supersedes / amends:** none
- **Decision:** Per-plan ladder for async result retention,
  customer-controllable per invocation via
  `InvokeRequest.RetentionSeconds`. Reapers prune terminal rows past
  `result_retention_until`. Default = plan-max ceiling (customers
  opt down, never up).

## Context

Gregale's `invocations` and `trigger_records` rows accumulated
forever — `pkg/sched/retention.go` only pruned `instances`. A
Hobby tenant running 1 RPS of async triggers for a year would end
up with 31.5 M rows of dead data the dashboard has to scan past
on every list.

The financial model (ex44_faas_financial_model.xlsx) treats async
job storage as **best-effort, customer-pruned** — the GB-h math
is for resident microVMs, not for archival of completed-job
results. So the platform's job is to give customers a knob
("keep my async results for N seconds after completion") and
back it with a reaper, not to build a job-results archive.

## Decision

### Per-plan ceiling

| Plan | `MaxAsyncResultRetentionSeconds` | Equivalent |
|---|---|---|
| Free | 86 400 | 1 day |
| Hobby | 604 800 | 7 days |
| Pro | 2 592 000 | 30 days |
| Scale | 7 776 000 | 90 days |

Customer can set `InvokeRequest.RetentionSeconds` to any value
**below or equal to** the plan ceiling. Above-ceiling requests
return `RFC 7807` `retention_exceeds_plan_max` (clamped on the
server-side, not rejected — server picks the plan max and returns
the actual stamped value in the response, mirroring how
`task_timeout_s` clamps today).

### Default

If the customer does not set `RetentionSeconds` on the invoke
request, the server stamps `result_retention_until = now() +
Limits.MaxAsyncResultRetentionSeconds` — i.e. the **plan ceiling**.
This is the "opt down" model: customers can shrink retention but
never grow it past the ceiling without a plan upgrade.

### Reapers

`pkg/sched/retention_invocations.go`:
- 60s tick (matches the existing `pkg/sched/retention.go` cadence
  for `instances`).
- Batches of 500 rows: `SELECT id FROM invocations WHERE state IN
  ('completed', 'dead_letter') AND result_retention_until <
  now() ORDER BY result_retention_until LIMIT 500 FOR UPDATE SKIP
  LOCKED`.
- Emits `invocations_reaped_total{state}` Prometheus counter.
- Partial index `(account_id, result_retention_until) WHERE
  result_retention_until IS NOT NULL` keeps the scan cheap.

`pkg/sched/retention_triggers.go`:
- 300s tick (trigger_records have larger terminal volume per
  row; a smaller cadence keeps PG lock contention low).
- Batches of 1000 rows.
- Emits `trigger_records_reaped_total{state}` Prometheus counter.
- Partial index `(app_id, result_retention_until) WHERE state IN
  ('succeeded', 'dead_letter')`.

### Schema

Migration `00518_invocations_async_fields.sql`:
```sql
ALTER TABLE invocations
  ADD COLUMN deadline_at TIMESTAMPTZ NULL,
  ADD COLUMN retry_policy JSONB NULL,
  ADD COLUMN result_retention_until TIMESTAMPTZ NULL;
CREATE INDEX invocations_app_deadline_idx
  ON invocations (app_id, deadline_at)
  WHERE state IN ('pending', 'dispatching');
CREATE INDEX invocations_acct_retention_idx
  ON invocations (account_id, result_retention_until)
  WHERE result_retention_until IS NOT NULL;
```

Migration `00521_trigger_records_async_fields.sql`:
```sql
ALTER TABLE trigger_records
  ADD COLUMN deadline_at TIMESTAMPTZ NULL,
  ADD COLUMN retry_policy JSONB NULL,
  ADD COLUMN result_retention_until TIMESTAMPTZ NULL;
CREATE INDEX trigger_records_app_deadline_idx
  ON trigger_records (app_id, deadline_at);
```

Migration `00524_trigger_records_retention.sql`:
```sql
CREATE INDEX trigger_records_app_retention_idx
  ON trigger_records (app_id, result_retention_until)
  WHERE state IN ('succeeded', 'dead_letter');
```

## Why not delete-on-complete by default

Two reasons:

1. **Customer UX** — the dashboard's "Recent invocations" page
   shows terminal results. A user who runs an async job expects
   to see the response for at least a few minutes; zero retention
   means a refresh loses the result instantly.
2. **Replay chain visibility** — `invocations.replayed_from_invocation_id`
   + `last_replayed_at` (added by ADR-134 PR-C) let the dashboard
   show "this invocation has been replayed 3 times in the last
   week". Zero retention breaks that audit trail.

The per-plan ceiling gives operators a knob without forcing every
customer through a retention-aware deploy workflow.

## Why not "unbounded for paid plans"

The financial model is RAM-and-runtime focused, not storage-focused.
Unbounded retention for Scale would let a single 100-app Scale
tenant accumulate terabytes of dead rows that the §12 dashboard
has to scan past on every load. The 90-day ceiling on Scale is
the longest we can offer without a dedicated archival story (S3
+ Iceberg or similar), which is post-M8.

## Spec compliance

Every `Limits` field added in ADR-135 lives in `pkg/api/limits.go`
and is mirrored in `limits_test.go`'s 4-row parity table
(Free / Hobby / Pro / Scale). Test `TestPlanLimitsMatchSpec` is
the gate.

## Consequences

Positive:
- Customers have a clear knob (`RetentionSeconds`) and the
  dashboard can show "this result expires in 23h 14m" in the
  invocation detail view (follow-up PR).
- Operators get a `invocations_reaped_total` counter that surfaces
  reaper health on the §12 dashboard.
- §17 gap `G-Async-Retention` closes.

Negative:
- The reaper is yet another periodic tick in `pkg/sched/loop.go`.
  Memory: existing tickers (`reaperT=10s`, the new
  `invocationsRetentionT=60s`, the new `triggersRetentionT=300s`)
  share the same `time.Ticker` pattern — no new abstraction
  needed.
- Three new partial indexes. Each is ~2-4 KB per 1000 rows;
  bounded by retention duration × tenant write rate.

## Follow-ups

- **Customer-facing retention editor on the dashboard** — a
  per-invocation retention slider; not in scope here.
- **Archival tier (post-M8)** — for Scale tenants who need > 90
  days of async result history, an S3-backed cold tier with
  on-demand hydration is the obvious next step. Not in scope
  here.
- **GDPR intersection** — `accounts.deletion_requested_at`
  already prunes customer data on account delete; the reaper
  must skip rows from accounts with `deletion_requested_at IS NOT
  NULL` so a deletion request doesn't get half-completed by the
  retention sweep. TODO checked-in here, fix in a follow-up PR.

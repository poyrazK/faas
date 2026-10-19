# Finished invocations without a horizon get the plan default retention

Date: 2026-10-19
Status: Accepted
Related: ADR-134 (async invocation retention and deadlines), ADR-179 (unified dead-letter ledger), migration 00550, spec §17

This record has a date-and-time identifier because date-only identifiers
collide under `make adr-number-uniqueness-check` when two ADRs land on the
same day.

## Context

`invocations.result_retention_until` is the horizon after which schedd's
`InvocationsRetention` reaper deletes a finished row. Migration 00550 and the
`MaxAsyncResultRetentionSeconds` comment say NULL means "use the plan
default", but the reaper only selects rows whose horizon is set. Only async
invoke, delayed tasks, durable-entity dispatch, API replays and operations set
one. Queue sends and the app inbox, crons, event deliveries and event routing
admission, async edge routes, inbound webhooks, object notifications, queue
canaries, commit sources and debug/mirror replays enqueue with NULL. Those rows
were only removed when the app, environment or account was deleted, so the
hot `invocations` table and its indexes grew for as long as an app lived.

Stamping each producer was rejected: there are about twelve call sites across
apid, gatewayd-internal and schedd, N-1 binaries keep writing NULL during a
rollout, existing rows are never repaired, and queued work could pass its
horizon before it ran. Stamping on every terminal transition was rejected for
the same reasons (eight or more transition paths). Deleting by plan inside the
reaper was rejected because one mixed-plan index would make every tick re-read
the long-retention rows that are not yet due.

## Decision

Each `InvocationsRetention.SweepOnce` tick, after the existing reaper runs,
schedd stamps a batch of finished rows (`completed`, `failed`, `dead_letter`,
`cancelled`, `superseded`, `expired`) that have no horizon:

- The horizon is `coalesce(completed_at, created_at)` plus the owner's plan
  `MaxAsyncResultRetentionSeconds` (Free 1 day, Hobby 7, Pro 30, Scale 90).
  The owner is the account or resource-owning organization in `account_id`,
  resolved like `ObjectUsageAccountPlan`. Plan values are passed from
  `pkg/api/limits.go`, so no limit lives in SQL.
- `failed` and `dead_letter` rows get at least `api.DefaultDeadLetterRetention`
  (30 days). The unified dead-letter projection replays the row in place and
  event execution recovery reads it for the 30-day receipt window, so a
  shorter plan horizon would break both on Free and Hobby.
- Rows are locked `FOR UPDATE SKIP LOCKED` in batches of 500, oldest first.
  The existing reaper then deletes them when due.

The reaper also includes `superseded` and `expired` keyed rows, which are
terminal but were never pruned even with a horizon. MemStore's reaper gains the
terminal-state filter PgStore already had. Due rows are deleted in one
statement per batch, falling back to per-row deletes on error, so the existing
backlog drains faster than one statement per row.

Migration `20261019104819437` adds two partial indexes with
`CREATE INDEX CONCURRENTLY`: unstamped finished rows by `created_at` (rows
leave it once stamped) and due finished rows by `result_retention_until`
(`invocations_acct_retention_idx` leads with `account_id` and cannot serve the
reaper's ordered scan).

## Consequences

- Customer-visible: cron run history, queue and inbox history, event delivery
  invocations, edge-route and inbound-webhook invocations now expire after the
  plan's result retention instead of never. Their attempt history, replay links
  and queue canary reports go with them (FK cascades).
- Duplicate detection that relies on a stored invocation (inbound webhook
  receipts, async route idempotency, object notification identities) lasts as
  long as the row. Event fanout is protected by its recipient checkpoint.
- The older execution-coverage status becomes `partial` for affected apps, as
  it already does when attempt history is pruned.
- Existing rows are stamped with horizons that may already be in the past and
  are reaped over the following ticks (500 per minute).
- N and N-1 schedd can run side by side: the old reaper deletes stamped rows
  and ignores the new states, and stamping is idempotent.

# Operator alert collection and retention health

Status: Accepted, 2026-10-15. Extends retained operator alert episodes and bundles.

## Decision

Separate alert-history retention from the Prometheus collection loop. Both run
immediately at apid startup and then every 30 seconds, in separate goroutines
under the daemon context. A missing Prometheus configuration disables collection
only. Retention still removes at most 500 expired episodes and 500 orphan rule
watermarks per pass, preserving the existing 30-day policy, strict cutoff,
collection-row lock order, and protection of recently observed firing episodes.
No new notification, dashboard, or rollout behavior is introduced.

Retention has a ten-second deletion budget. On failure, record the attempt
using a fresh five-second budget derived from the daemon context. Shutdown
cancellation stops this extra write. Failure to persist the receipt cannot
invent success: the previous timestamp eventually becomes stale and the
process metric records a failed attempt. Logs use closed reason strings.

## Durable receipts and reads

An additive replay-safe migration creates a singleton
`operator_alert_retention_health` row. Cleanup's start timestamp, last success,
and count of pruned episodes commit in the deletion transaction. Lock the
receipt row after the collection row, before deleting; a missing receipt row
aborts cleanup. A receipt failure rolls deletion back. Failure receipts advance
only the attempt timestamp;
delayed results cannot overwrite newer health. Times use PostgreSQL microsecond
precision. The last-pruned count describes the last successful episode batch,
not rules, total storage reclaimed, or a lifetime deletion count.

A SQLC query reads the receipt, exact expired-episode count, and oldest eligible
timestamp in one statement snapshot. It uses the same indexed
`coalesce(resolved_at,last_seen_at) < cutoff` predicate as deletion. The count
can exceed the batch cap; it is an observation, not a deletion promise. Rule
watermarks are still cleaned but are not included in the episode count.
Memory storage mirrors the contract under one lock.

Admin health reads use a five-second shared maintenance budget. Required source
failures return 503; they never become a healthy zero backlog. Existing admin,
MFA and email-allowlist gates apply. The response adds `alert_retention` with
state/reason, snapshot time/cutoff, attempt/success clocks, nullable last-pruned
count, exact expired-episode count, and oldest eligible time. States are
`never_run`, `current`, `failed`, `stale`, and `unknown` for future observations.
`current` means a recent successful batch, even when backlog remains.

`alert_history.collection_state` adds `disabled`, `never_collected`, `current`,
`failed`, `stale`, or `unknown`. Preserve the original current/unknown evidence
state and reason vocabulary; a disabled collector is unknown, and an initial
failed poll has explicit collection_state=failed even with no successful
history. Disabled status comes from this apid process's configuration, not a
database flag. Retained incident status still uses its existing observation
contract and never manufactures recovery.

`gregalectl obs health` renders both states and timestamps. Incident bundles
retain the new health DTOs and summarize explicit collection/retention states
and nullable backlog. Old servers without metadata remain unknown. Bundle v1
changes are additive; its separate-read and privacy limitations remain intact.

## Metrics

Register these on apid's existing registry, with closed operation labels
`collection|retention` and outcome labels `success|failure` only:

- `apid_operator_alert_maintenance_attempts_total{operation,outcome}`
- `apid_operator_alert_maintenance_duration_seconds{operation}`
- `apid_operator_alert_maintenance_last_attempt_timestamp_seconds{operation}`
- `apid_operator_alert_maintenance_last_success_timestamp_seconds{operation}`
- `apid_operator_alert_collection_enabled`

Counters and durations are process-local attempts, including cancelled/failed
attempts, not global episode totals. Timestamp gauges record the start time
after an attempt completes; success does not advance on failure. Gauges start
at zero after restart until a matching attempt completes; durable API receipts
preserve prior history. Enabled=0 distinguishes disabled collection from silence.
No incident, account, node, URL, raw error, or secret enters a metric label.
Rules/dashboards and rollout remain the other agent's scope.

## Validation

Memory/PostgreSQL tests cover strict expiry boundaries, bounded deletion,
failure/delayed receipts, cancellation, receipt/deletion atomicity and restart
persistence. Domain tests cover disabled collection with independent cleanup,
failed attempts, health states, and bounded metrics. API tests cover exact
backlog and fail-closed reads, with existing authorization regressions retained.
CLI/bundle tests preserve states and unknown legacy metadata. Migration replay,
schema/SQLC drift, policy checks, and configured lint validate the change.

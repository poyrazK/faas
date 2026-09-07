# WakeTimelineEndpointRollout

Source: issue #517 PR-C / ADR-064. Spec: §6.1 (events table),
§12 (wake-latency panel). Migration: 00113_events_wake_id_idx.
Endpoint: `GET /v1/apps/{slug}/wakes/{wake_id}/timeline`.
Severity: info (this is a canary rollout, not an incident).

## Scope

PR-C of issue #517 ships the customer-facing wake-timeline
endpoint. The endpoint reads from the `events` table via the
new partial jsonb expression index `events_wake_id_idx`
(migration 00092). The migration is `CREATE INDEX IF NOT EXISTS`
and uses Postgres' default btree — it cannot block writes, but
the build is O(n_rows) on the events table and can saturate
disk I/O on a busy audit log.

## Why a canary

The events table on a busy Hobby/Pro/Scale box carries 10⁶+ rows
during a typical weekday. The partial index only materialises
rows whose `data->>'wake_id'` is non-NULL (1 per wake phase, ~13
per cold wake), so the index size is bounded by the wake-envelope
volume — not the audit-log volume. Even so, the index build
itself is O(n_rows) and can hold a `ShareUpdateExclusiveLock`
if not run with `CONCURRENTLY` (it is, via the migration runner —
verified in `pkg/migrations/run.go`).

## Pre-flight

1. Confirm the staging box has the migration applied:
   ```sql
   SELECT indexname, indexdef
   FROM pg_indexes
   WHERE indexname = 'events_wake_id_idx';
   ```
   Expected: one row, `USING btree ((data ->> 'wake_id'::text))`
   with the `WHERE ((data ->> 'wake_id'::text) IS NOT NULL)`
   partial clause.

2. Confirm the staging endpoint returns a 200 for a known wake_id
   (the test in `cmd/apid/handlers_wake_timeline_test.go`:
   `TestListWakeTimeline_HappyPath` covers the canonical shape).
   ```bash
   curl -sS -H "Authorization: Bearer $FAAS_TOKEN" \
     https://staging.example.com/v1/apps/$SLUG/wakes/$WAKE_ID/timeline | jq
   ```

3. Confirm the §12 wake-latency panel populates from
   `wake_phase_duration_seconds` (the metric was
   pre-instantiated in PR-C commit 1 — every daemon's
   `wire.NewOpsMetrics` registers the closed tuple even if it
   doesn't emit that phase).

## Canary rollout (10% Hobby/Pro/Scale)

The endpoint is **on by default** for every account — there's no
`enable_wake_timeline` flag (the plan's staged-rollout knob was
designed out in commit 8 because the read path is a partial index
and the cost is bounded by the index size, not the audit-log size).
The "canary" is therefore an **observability ramp**, not a
feature-flag ramp:

1. **Hour 0–1**: enable the endpoint on staging. Watch
   `apid_ops_total{op="list_wake_timeline"}` for traffic
   volume. Expected: ≤ 10 req/min on a staging-equivalent of
   the production Hobby/Pro/Scale mix (the dashboard polls
   once per second on each app per user).
2. **Hour 1–2**: enable the endpoint on production. Same
   metric should rise to ~50 req/min at peak (the load test
   in `pkg/load` pumps 1000 req/s on the wake endpoint).
3. **Hour 2–24**: monitor `apid_op_duration_seconds{
   op="list_wake_timeline",quantile="0.99"}` p99. The index
   makes the read an O(limit) cost (200 default, 1000 max)
   regardless of the events table size. Expected p99: ≤ 5ms.

## Rollback

Roll back through the normal release/migration pipeline so schema state and
the goose ledger stay aligned. If an index emergency makes that path too
slow, declare break-glass and use the reviewed procedure in
[database repair](../break-glass/database-repair.md); do not run ad-hoc SQL
from this runbook.

The endpoint reads from the index when present, but the read
path (`pkg/state/queries.sql::ListEventsByWakeID`) is a plain
`WHERE data->>'wake_id' = $1` query — it falls back to an
unindexed scan automatically. Without the index, the read cost
grows to O(n_events) but the endpoint still returns correct
results. The canary therefore isn't a hard switch — it's a
progressive index-build that the on-call can drop if the partial
index bloat exceeds the 160 MB alert threshold (see
`FaasSnapshotFleetHigh.md` for the analogous snapshot-fleet
threshold).

## Done

The endpoint is "ramped" when `apid_ops_total{op=
"list_wake_timeline"}` shows sustained traffic from the
dashboard (≥ 100 req/hour per §12). At that point, remove this
runbook from the on-call rotation and move the SLO to the
canonical `apid_op_duration_seconds` p99 panel.

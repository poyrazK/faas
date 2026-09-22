# ADR-221 · Replay-safe mirror rollups

- **Status:** proposed
- **Date:** 2026-09-22
- **Amends:** ADR-133 D15; issue #72 rollup/recovery correctness
- **Decision:** retain additive hourly counters, but atomically record which
  invocation rows have contributed to them.
- **Why:** append-only input alone does not make an additive aggregate
  idempotent. Restart replays double-counted results, narrow tick windows lost
  failed/late work, and retention deleted results before they were summarized.

## Decision

`mirror_invocation_results.rollup_counted` defaults to false. A generated sqlc
statement locks pending rows with `FOR UPDATE SKIP LOCKED`, marks them counted
in a data-modifying CTE, and adds only those returned rows to hourly summaries.
Both changes commit or roll back together. Concurrent workers may split the
input, but each result contributes once. Hour buckets explicitly use UTC.

Every tick, including startup, revisits all pending rows completed before the
tick. A partial index excludes counted rows. There is no timestamp watermark
that can strand a delayed commit or skip an outage. Retention deletes only
counted rows older than seven days; failure delays deletion instead of losing
history. Payload and comparison fields remain immutable; only the receipt is
updated. Schedd remains the rollup owner, with no new daemon or customer API.
Its startup is independent of the optional gateway metrics scraper.

## Cutover and rollback

This is a coordinated writer cutover, not a mixed-version rolling upgrade:

1. Stop **all legacy schedd mirror-rollup writers** before applying the
   migration. Do not run an old binary against the expanded schema afterward.
2. Apply the migration. It takes exclusive locks on the raw and summary tables,
   baselines retained results and installs receipts in the same transaction.
   Budget a maintenance window proportional to the retained ledger size; the
   backfill scans and updates existing raw rows and temporarily blocks inserts.
3. Start the new schedd binaries. New inserts default to uncounted and are
   drained on startup. Migration replay detects the existing receipt column
   and leaves counted/archived history intact.

Whole UTC hours strictly inside the seven-day retention boundary are rebuilt
from retained rows, removing misaligned recent legacy buckets first. Older
buckets preserve the larger of the existing and
retained totals, and buckets with no raw rows are untouched. This avoids
shrinking archived history, but **cannot exactly repair legacy overcounts,
missing contributions or non-UTC buckets once their raw evidence is gone**.
It does not promise retrospective recovery of already-deleted data.

Rollback requires stopping new writers before the Down migration and restoring
old binaries together. It restores the old, non-idempotent behavior; prefer a
forward fix. Do not remove receipts while new workers are running.

## Consequences and alternatives

One additional update and a partial-index entry per invocation buys durable
deduplication. Prolonged database errors retain raw rows beyond seven days;
operators must monitor existing rollup warnings and database capacity.

Recomputing a partial hour would discard earlier contributions. Recomputing
only retained data would shrink archived hours after pruning. An in-memory
cursor cannot handle process restarts or transactions committed out of order.
These alternatives do not satisfy the existing lifetime-summary contract.

## Verification

Real PostgreSQL regressions cover replay, overlapping windows, concurrent
workers, rollback, delayed inserts, UTC bucketing, retention, long outages and
legacy migration/replay. Unit tests cover loop cancellation and error handling.

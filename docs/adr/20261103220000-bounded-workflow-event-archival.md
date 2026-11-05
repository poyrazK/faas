# ADR 20261103220000 · Bound workflow event archival

- **Status:** proposed
- **Date:** 2026-11-03
- **Related:** ADR-081 (durable workflows), ADR 20261018084150 (release compatibility)

## Context

Deleting an expired run copied all its events into the archive in the run
deletion transaction. A run can have many events, so the run batch limit did
not bound the work or the size of that transaction. Events have a longer
retention period than runs, and active or resumable runs must continue to see
their original events.

## Decision

Keep event rows in `workflow_events` when their run is pruned. A small owner
record associates those rows with their app and marks them for archival. The
retention worker moves at most 64 events per pass into the existing
archive and immediately schedules another pass when more work may remain.
The worker advances bounded owner and event cursors past locked rows and
conflicting archive IDs. A conflict keeps both copies and is reported while
other events continue. Empty-owner cleanup examines only the selected owner
page. A restart starts a new scan from the oldest owner. The event cursor's
`(run_id, id)` index is built concurrently by an additive migration; the full
release applies it before starting the updated worker.
Both event tables remain visible through the existing event-history queries.
Events of runs that still exist are never moved by this process.

The migration replaces the event-to-run cascade with database triggers that
check event ownership, create the owner, and remove retained live events when
the owner is deleted with its app. Existing event rows acquire owners as their
runs are pruned. Run IDs with retained events cannot be reused. A failed
archive insert rolls back the corresponding live-event deletion.
The run ID and app cannot be changed while live events belong to that run;
event insertion and ownership changes are serialized by the run row lock.

This is a contract migration and requires a full release. Existing binaries
can continue to read live events, including those belonging to pruned runs,
until the new retention worker moves them. The rollback keeps the owner and
triggers: restoring the old cascade would require an unbounded data move or
would discard retained events.

An older scheduler can read the retained rows but cannot drain the new owner
backlog. During a prolonged binary rollback, event expiration for those rows
can therefore be delayed until the updated scheduler is restored. The full
release must bring up the updated scheduler and check its retention backlog.

## Consequences

Run deletion no longer copies or cascades an unbounded number of event rows.
An interrupted worker leaves live rows and their owner available for retry;
event retention still removes evidence based on its own age. App deletion
continues to remove both archived and not-yet-archived event data. Permanent
app deletion can itself remove many events, as it could before this change.

# ADR: Restart-safe Node recovery alert checkpoints

- Status: Accepted
- Date: 2026-10-17

## Context

The Node recovery watch emits local transitions but retains no incident identity
or notification state after a restart. A crash between detection, notification
and destination acknowledgment can lose a notification or create duplicate
incident openings. Node deadline watches already use application-persisted
checkpoints and a bounded notification outbox.

## Decision

Add a separate opt-in checkpointed recovery watch; preserve the ordinary watch.
Bind a versioned checkpoint to normalized owner scope and full effective policy.
Validate and reconstruct known metadata, including notification lineage, timestamp
high-water mark, safe sequence bounds, condition identity and acknowledgment order.
Keep monotonic timers and unfinished confirmation proof process-local.

Assign stable session/opening-sequence incident IDs. Retain unresolved identity
through evidence loss and restart, reset proof, and require fresh complete advancing
observations to reconfirm or resolve. Reconfirmation does not enqueue another
opening. Unknown evidence is progress metadata; only opened/resolved lifecycle
notifications enter the durable outbox.

Bound pending notifications and reserve one queue and sequence slot for every
open incident's resolution. Block new openings when capacity or sequence space is
insufficient. Accept all-or-nothing exact incident/sequence acknowledgments,
requiring opening acknowledgment before or together with resolution. Retain a
bounded history of acknowledgment receipts for idempotent retry.

Run optional acknowledgment polling and initial progress before the first health
query, allowing pending replay while the health database is unavailable. Require
an awaited progress callback to atomically persist the whole checkpoint before
attempting notification. Use detached snapshots and one fenced application writer.
Callback failure stops with a recoverable metadata result and no raw cause.
Cancellation waits for active work and returns a final unknown snapshot without
resolving or automatically acknowledging incidents.

## Consequences

Applications gain restart continuity and replayable notification identity without
platform tables, routes, files or installed services. Storage fencing, atomic
commit, downstream deduplication and notification transport remain owner concerns.
At-least-once replay can repeat confirmed notifications when acknowledgment is
uncertain; it is not a storage/transport transaction. Validation cannot authenticate
or detect rollback to an older valid checkpoint. A full outbox does not drop
notifications, and fresh evidence remains necessary after acknowledgment frees
capacity. The format is separate from deadline/Go/CLI checkpoints.

See the [checkpoint guide](../event-acknowledgment-node-checkpoints.md).

## Validation

Use SDK build/generation, package/export inclusion, and static source/documentation
checks. No new tests, test assertion changes or local test execution are part of
this increment. Hosted acceptance is required for restart/replay, storage fencing,
cancellation and timing before release.

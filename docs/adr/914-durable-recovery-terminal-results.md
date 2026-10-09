# ADR-914: Durable terminal recovery results

Date: 2026-10-09
Status: Accepted

## Context

ADR-807 attributes handler observations to each admitted replay's exact identity.
Its reads rely on invocation rows or retained attempt history, so known outcomes
can disappear after those records are pruned. ADR-913 protects receipts during
pending recovery but deliberately does not preserve execution evidence.

## Decision

Create metadata-only `event_recovery_execution_results`, owned by the composite
recovery item key with cascading deletion. Store exact replay id, generation and
creation time, terminal state, attempt count, optional actual completion time,
capture time and original evidence source. There is no invocation/attempt foreign
key and no payload or error data. Enumerated states and sources, nonnegative
counts/generations and timestamp checks constrain evidence. Register every column
as operational in the environment clone schema registry. Add a partial queued
item identity index for transition capture.

Capture confirmed succeeded, failed, dead-lettered, expired, cancelled or
superseded invocation states atomically through database transition triggers.
Reject uncertain invocation outcomes and future completion timestamps. Capture
old terminal evidence before deleting an invocation or replacing its exact
identity, and capture already-terminal invocations when replay tracking is
registered. Scope every write to queued execution-mode items whose account,
app, id, generation and creation time match. Inserts are ordered by item key,
and conflict does nothing: first confirmed terminal evidence is immutable.
Capture neither locks recovery jobs nor updates admission state. Select
matching items with `FOR KEY SHARE` before inserting evidence, so concurrent
job pruning cannot invalidate the item FK after selection and abort a handler
terminal transaction. These locks are compatible with ordinary item updates;
recovery workers retain their existing job-to-invocation lock order. No asynchronous
capture worker or write-on-read path is added.

Backfill existing tracked items only from currently retained exact terminal
invocations, or, when no exact invocation remains, the latest still-retained
attempt for that generation, account and app, begun no earlier than the tracked
creation time. Only finished succeeded/failed/dead_letter/cancelled attempts
qualify. A newer running/retry/unknown attempt prevents using an older terminal
record. The retained invocation must also prove the original creation time for history
fallback, preventing attribution to a reused UUID/incarnation. An exact
uncertain invocation blocks history fallback. Never reconstruct
legacy replay identities, expired attempt evidence or already-lost outcomes.

Existing read-only repeatable-read job and item observations prefer a matching
saved result over live records and history. Preserve the existing fallback for
unsaved work and uncertainty. Public `execution.source` gains `recovery_result`,
with optional `recorded_at` and `evidence_source`. The observation timestamp
remains the read time. Summaries expose `saved_results`, a subset of tracked_count
rather than a state bucket. State counts still sum to tracked_count/queued_count.
Expose source/capture time in CLI item output and saved count in job status, with
Go, Node and Python SDK parity. No endpoint or authorization changes are needed.

Memory capture shares the invocation mutation mutex. Preserve old generation
results before replacement/deletion and capture terminal writes and identity
registration. Store separate immutable result values, copy response timestamps,
rollback result-map changes with staged routing admission, and remove results
when pruning their owning job. Production migration backfill applies to Postgres;
memory stores do not retain data across process upgrades.

## Consequences

A saved terminal outcome survives invocation and attempt pruning while its
recovery job remains retained. Results inherit the existing 30-day terminal job
retention, not 30 days from handler completion, and add at most one metadata row
per selected admitted item under existing recovery limits. They do not pin
execution records or receipts, extend delivery age, certify exactly-once effects,
make uncertain effects certain, or infer a new generation's outcome. Admission
completion and cancellation still do not terminate already-admitted handlers.

Apply the migration before upgraded API/scheduler binaries. Upgrade SDK
consumers that validate `execution.source` to accept `recovery_result` before
upgrading API binaries. Downgrade binaries
before rollback; Down removes the snapshot table and therefore discards saved
results, potentially restoring unknown observations when original evidence is
already gone. No production migration, replay or notification is performed while
implementing this change.

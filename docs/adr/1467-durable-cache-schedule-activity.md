# ADR-1467: Durable environment cache schedule activity

Status: Accepted

## Context

Agents can discover schedules and inspect exact receipts, but need to resume
following outcomes after reconnecting without polling every schedule UUID.
A global identity sequence is not a safe cursor when transactions commit out of
allocation order. Execution receipts also must remain separate from gateway
convergence, and the new feed must preserve existing write ownership and retries.

## Decision

Add an apid-owned event ledger and one transactional sequence counter per immutable
app/environment. Allocate using an upsert row lock and append event metadata in
the same schedule transaction. Counter updates roll back with event or intent
failure and hold their lock through commit. Read the head and limit+1 events from
one repeatable-read snapshot, requiring contiguous ascending sequences. Bound
pages at default 20/max 50 in api/limits.go. Always return an account/app/project/
environment-bound cursor, including on empty pages; reject ahead-of-head cursors.
Cursors are continuation metadata, not authorization grants. Recheck live owner
identity and current exact named-environment read consent on each native/MCP read.

Publish accepted, rescheduled, cancelled, executed and failed transitions. A new
schedule emits acceptance once. Cancellation emits only when it changes pending
state. Replacement emits the cancelled original linked to its successor then the
accepted successor linked to its predecessor, atomically. Receipt replays return
without emitting; fenced worker completion appends once, and stale tokens append
nothing. Claims/reclaims are not completion events. Retain compact action type,
UUIDs, sequence, execution/event timestamps, stable failure codes and confirmed
policy revision/purge generation. Exclude rule bodies, paths, raw exceptions and
gateway identities. Never infer fleet convergence from an executed event.

Worker completion now takes account, app and environment locks before changing
the schedule and appending activity, matching intent-write lock order and avoiding
reverse lock acquisition through event foreign keys/counters. These locks do not
apply eligibility checks, so committed receipts and failed outcomes can still be
recorded after eligibility changes or soft deletion. The action itself continues
to use existing native eligibility and revision guards. MemStore publishes under
its existing mutex and returns copied immutable event snapshots. Physical owner
deletion cascades ledger and counter rows; recreated slugs have new UUIDs and
cannot read old events or reuse old checkpoints.

## Consequences

No push destinations, recurring notifications, event filtering or pruning are
introduced. Ledger storage grows until physical owner deletion. Each page is a
snapshot of committed activity; a tail checkpoint remains valid for later events.
The feed begins at upgraded writers and fabricates no historical events. Roll
out migration 20261016153051018 and all apid writers/workers before enabling feed
consumers; establish the initial baseline through schedule discovery. Mixed old
writers cannot satisfy the publication guarantee. Full schedule state and sampled
gateway evidence remain available through their existing scoped read APIs.

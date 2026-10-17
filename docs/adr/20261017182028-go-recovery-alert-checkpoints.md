# ADR: Restart-safe Go acknowledgment recovery alerts

- Status: Accepted
- Date: 2026-10-17

## Context

Go's ordinary acknowledgment-intent health watch keeps condition state in memory.
Restart loses unresolved identity and pending alerts. Node and Python offer
application-persisted checkpoints with exact acknowledgments and fresh
confirmation after resume. Go needs that boundary with native typed values,
`database/sql`, context cancellation and result-plus-error APIs.

## Decision

Add an opt-in checkpointed watch and pure validation/acknowledgment helpers.
Version 1 has a distinct Go kind, normalized owner, complete effective policy,
canonical UUID session and safe bounded notification sequences. Use typed integer
JSON, UTC `time.Time` timestamps and explicit nanosecond policy durations so
ordinary Go sub-millisecond options survive policy binding. Validate bounded
conditions/notifications, timestamps, incident lineage and acknowledgment order;
deep-copy all snapshots. Validation cannot authenticate or detect stale storage.

Persist the database timestamp high-water mark, historical counts, unresolved
IDs, pending lifecycle events and acknowledged receipts. Resume resets state to
unknown and discards all monotonic proof. Fresh complete advancing samples must
sustain opening, reconfirmation and resolution. Breach reconfirms an unresolved
incident without opening a second one; missing evidence never deletes identity.

Only opened/resolved notifications enter the outbox. Bound pending-plus-reserved
capacity and acknowledged history to 1024 each. Opening reserves queue/sequence
space for eventual resolution. Capacity exhaustion blocks new openings without
dropping entries. Exact acknowledgments are all-or-nothing, idempotent against
retained receipts, and enforce opening before/together with resolution.

Require sequential `OnProgress`, with initial detached progress before health
reads. Applications must atomically persist each complete checkpoint before any
notification, fence one writer and retain nondestructive acknowledgment ledgers.
Optional acknowledgment polling applies the same pure helper between reads.
Reuse the existing health read, policy and evidence guards without changing them.

Return detached progress with fixed callback phase/error or native context
cancellation. Active reads/callbacks complete before return; create no worker
goroutine. Cancellation resets confirmation proof while retaining pending state
for independently bounded fenced shutdown persistence. It resolves/acknowledges
nothing. Arbitrary callback causes and caller cancellation causes are omitted.

## Consequences

Applications can resume stable incidents and replay pending metadata alerts with
at-least-once delivery and downstream deduplication. Storage, available hosts,
fencing and notification transport remain application-owned. Failed saves may
already have committed and require reconciliation against current fenced state.
No platform schema, route, registry, migration or notification transport is added.
Ordinary watches, callback recovery and business/delivery semantics are unchanged.
See the [Go checkpoint contract](../event-acknowledgment-go-checkpoints.md).

## Validation

Use standalone SDK/example builds and vet, static Go format/lifecycle/copy review
and unchanged read/schema comparisons, plus schema/ADR/documentation checks.
Add no tests, change no assertions and run no local tests. Hosted restart,
notification replay, storage fencing, capacity, timing and cancellation
qualification remains pending before release.

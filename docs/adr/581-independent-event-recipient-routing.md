# ADR-581: Independent event recipient routing and recovery

- **Status:** proposed; implemented behind an opt-in adoption flag
- **Date:** 2026-10-04
- **Decision:** Keep the durable event receipt and immutable acceptance snapshot,
  and give every captured recipient its own routing lease, retry schedule, and
  replay generation in `event_fanout_recipients`. Adopt snapshot-backed receipts
  only when `FAAS_EVENT_RECIPIENT_CLAIMS_ENABLED=1` on schedd. The default remains
  the existing whole-receipt worker until the rollout is qualified.
- **Why:** Recipient outcomes and invocation retries were already independent,
  but routing shared one event claim and backoff. A failed recipient could not
  be replayed while a sibling was still routing. An exhausted recipient also
  retained its exhausted retry counter after replay.

## Routing ownership

Acceptance still captures enabled source/type candidates, data filters, work
policies, and object-notification configuration in the publish transaction.
Adoption atomically materializes those candidates and prior checkpoints, starts
a fresh routing budget while preserving lifetime attempts, then clears the
parent claim. It leaves the parent `processing` until every recipient
is `enqueued`, `filtered`, or `failed`. An empty snapshot settles immediately.
Older receipts without snapshots keep their existing routing behavior.

Workers claim due or expired recipient rows with `FOR UPDATE SKIP LOCKED` and
fresh five-minute lease tokens. Routing runs outside the claim transaction.
Completion locks the parent before its recipient, checks the token, generation,
and lease against the database clock after obtaining the lock, then commits the
recipient state, public checkpoint, history, and aggregate receipt together.
The short parent lock serializes checkpoint updates, not application routing.

Each recipient backs off independently from five seconds to five minutes and
has at most twelve failed routing attempts per replay generation. Successful
enqueue after a lost checkpoint remains recoverable: invocation identity still
derives from account, source, event ID, and subscription ID, without the lease
or replay generation. Expired workers cannot acknowledge a newer claim.

Single-recipient replay and bounded retryable batch replay can reset a failed
recipient while siblings are pending or leased. Replay increments its generation,
resets its retry budget, and schedules it immediately. Lifetime attempt counts
and the failure that prompted replay remain in history. Batch selection stays
oldest first across both receipt modes and commits atomically. Legacy receipts
retain their existing whole-event replay restrictions.

## Public semantics

- Publish acceptance acknowledges durable routing intent. `enqueued` means an
  invocation exists; parent `delivered` means all routing candidates settled.
  Neither means the application handler completed.
- Handler retries, dead letters, and invocation replay keep their existing
  lifecycle. Pre-invocation routing failures remain visible in event deliveries
  and fanout history and use recipient replay.
- Publish deduplication remains account/source/ID scoped, with conflicting type,
  schema version, or data rejected. Routing recovery preserves invocation ID;
  handler execution is at least once, so handlers must make side effects
  idempotent. There is no exactly-once side-effect guarantee.
- There is no global, per-source, or per-subscription FIFO guarantee. Independent
  backoff, lease recovery, and replay can reorder enqueue and completion. Existing
  work policies constrain dispatch concurrency within a key; they do not restore
  publication order.
- Settled receipts and their attempt history retain the existing thirty-day
  retention policy. Unfinished recipients keep the parent active and cannot be
  removed by settled-receipt pruning; recipient rows cascade with the parent.

## Rollout and rollback

1. Apply the additive migration and deploy compatible apid and schedd binaries
   everywhere with adoption disabled. No existing receipt is rewritten by the
   migration. Confirm schema and generated SQL agree.
2. Qualify publish, independent replay, restart after enqueue, lease expiry,
   retention, and concurrent operators in staging, then enable adoption on
   schedd. There is no automatic production enablement in this change.
3. Disabling the flag stops new adoption. Compatible workers always continue
   draining adopted receipts; disabling it does not abandon pending work.

Old schedulers cannot claim adopted active parents because their parent lease
is null. Old API replay code does not understand recipient generations, so do
not enable adoption until every API writer is compatible. After adoption, keep
compatible binaries through the receipt retention period. The down migration
refuses to remove ownership while any adopted receipt remains; draining routing
alone does not make retained replayable receipts safe for old writers.

## Evidence and remaining qualification

State tests cover distinct concurrent claims, selective single/batch replay
while a sibling is leased, generation fencing, expiry before and after reclaim,
and retention in memory and real PostgreSQL. Scheduler tests cover independent
backoff, immutable recipients, restart with adoption disabled, lost enqueue
acknowledgement without duplicate invocations, and a fresh replay retry budget.
These gates qualify routing storage and recovery; staging rollout and real
handler execution/DLQ recovery remain operational acceptance work.

Local qualification used Go 1.25.13 and PostgreSQL 16 on macOS arm64. Recipient
state, migration replay, and scheduler routing regressions passed with the race
detector; lint passed for the changed packages. SQL regeneration, migration
structure, environment contracts, OpenAPI validation, and documentation links
passed. The repository-wide unit run was interrupted by exhausted disk space.
Full lint reported ten unused symbols in unchanged guest/VM code on macOS;
repository-wide Linux CI and staging acceptance remain required before rollout.

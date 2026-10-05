# ADR-588: Atomic event routing handoff

- **Status:** accepted
- **Date:** 2026-10-05
- **Decision:** Admit a captured application-event recipient's plain invocation,
  keyed invocation, or cancellation and record routing success in one transaction.
- **Why:** Deterministic delivery IDs alone cannot close the crash gap between
  enqueue and checkpoint, especially when delivery rows are later pruned. Stale
  routing workers could also supersede or cancel pending work before losing their
  completion lease.
- **Consequences:** Routing recovery reads the committed checkpoint after an
  uncertain response. Delivery pruning cannot authorize another admission while
  the event receipt is retained. Handler execution remains at least once.

## Transaction and authority

`PublishedEventRecipientAdmissionStore` accepts only outbox ID, subscription ID,
claim token and generation. PostgreSQL derives the target, envelope, filter,
delivery ID and captured work policy from durable acceptance metadata. Generation
zero denotes the whole-event lease; positive generations denote ADR-581 recipient
leases. Old snapshots without work-policy capture preserve live-binding lookup.

Keyed invocation and cancellation use the existing shared lane transaction
helpers. Lock order is lane, event receipt, recipient when adopted, then current
owning application. The receipt serializes recovery and replay; the lane preserves
ordering with explicit invocations and broker records. Target ownership is checked
under an application share lock. A missing, deleted or transferred target cannot
receive admission from its former account.

Lease validity uses PostgreSQL `clock_timestamp()` after all locks are acquired
and again after the target mutations and history write. Expiry or failure rolls
back enqueue, supersession, cancellation receipt and affected broker records,
lane sequence, checkpoint, history and receipt settlement. Recipient completion
also checks the replay generation. MemStore mirrors the transaction under its
mutex and restores staged admission maps on failure or expiry.

The scheduler sends the invocation wake only after a successful commit that
created an invocation. Wakes remain advisory; polling drains committed work.
Successful admission completes its own checkpoint and aggregate settlement, so
the scheduler does not perform a second completion write.
The retry-progress writer also checks the lease and refuses to overwrite an
admitted or filtered checkpoint, including when a whole-event lease remains
active for siblings after an unknown commit response. Storage errors before
filter matching retain retry eligibility rather than becoming terminal nonmatches.

## Recovery and retention

An `enqueued` or `filtered` checkpoint is authoritative for that captured target.
A duplicate read in the same routing mode and recipient generation returns the
stored result without creating work, incrementing the lane or appending history.
The completed lease token may have been cleared; duplicate reads have no side
effects. New admission always requires the exact live lease token and generation.
A pruned cancellation receipt cannot authorize cancelling work admitted later.

For historical partial handoffs, a retained owned invocation with matching event
payload and source, or an owned cancellation receipt, can reconcile the missing
checkpoint. Its creation must be at or after event acceptance. Such a committed
row remains proof even if retention removes it while reconciliation runs. Missing
historical rows without a success checkpoint cannot be inferred after pruning.

The marker lives in the existing receipt checkpoint; no migration or new retention
window is introduced. Delivered event identities retain their existing thirty-day
window. Receipt pruning ends this guarantee, and reuse after that window follows
the existing event identity contract. `enqueued` means routing admission, including
cancel-pending work; it does not mean successful handler execution.

## Compatibility and scope

Both whole-event snapshot routing and already-adopted independent recipients use
the handoff. ADR-581 adoption still defaults off. Existing stores without the new
capability retain their previous scheduler path. All scheduler writers must be
upgraded before relying on the atomic guarantee; older binaries can still produce
partial handoffs. Acceptance snapshots and the public receipt APIs do not change.

Receipts predating recipient snapshots continue current-subscription routing.
Object-notification queue and webhook destinations retain their specialized
delivery paths. They are outside this application-invocation handoff.

The envelope and matcher implementation moves to `pkg/eventcontract`, which has
no state or event-emission dependencies. `pkg/events` keeps type aliases and
validation wrappers, preserving its existing callers and conformance tests.

## Qualification

Tests cover both routing modes, concurrent admission, recovery after pruning,
shared lane sequencing, cancellation of pending work, and later-work protection.
Real PostgreSQL tests inject history failures and lease expiry while waiting on
the application lock. They assert rollback of target rows, cancellation receipts,
checkpoints and attempt history, followed by successful recovery where applicable.

PostgreSQL 16.15 qualification passed for both routing modes, including expiry
during the history write after invocation and broker mutations, pruning of
cancellation receipts, and current app ownership changes. Existing receipt,
replay, work-policy and shared broker-lane regressions passed. Full portable
`pkg/state`, `pkg/events` and `pkg/api` suites passed; focused scheduler tests
included object delivery end to end in memory and PostgreSQL. Scoped lint,
`sqlc-check` and diff whitespace checks passed. Large state and scheduler builds
used disabled test inlining/debug information to fit the local shared machine;
PostgreSQL tests used an isolated task-owned cluster. Linux CI and deployment
qualification remain rollout gates; independent-recipient adoption stays off by
default.

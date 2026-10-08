# ADR-719: Subscription pause and paced drain

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Add durable operator controls to pause captured application subscription admission and resume it with a persisted per-consumer drain rate.
- **Why:** Operators need to contain a consumer outage while preserving event acceptance and independently serving other consumers.
- **Consequences:** Controls apply to publication snapshots and application backfill recipients; pause does not disable matching or stop admitted invocations. Pending receipts consume existing account event storage budgets and remain retained until routing settles. Resume rates range from 1–100 new admissions per second (default 10); zero removes pacing. Rate limits continue after draining.
- **Rejected alternatives:** Disabling matching would lose the consumer's events during maintenance. Pausing the account would stop unrelated consumers. A client-side delay or in-memory budget would lose behavior on restart or with multiple workers.

## Transactions and persistence

A separate operational control row belongs to account, app, and captured
subscription ID. It intentionally has no subscription foreign key: removing a
declaration must not release retained paused recipients. App/account deletion
cascades controls, and soft-deleted apps bypass controls to allow normal
unavailable-target settlement. Controls can be resumed after subscription
removal. Redeployment does not overwrite controls, and environment cloning does
not copy operational pause state.

An advisory transaction lock serializes each consumer's pause/resume with its
admission transaction, even before a control row exists. Admission reserves
rate permits in that transaction after capacity checks. A pause response follows
earlier admissions; invocation execution continues independently. Repeated
resume with the same active rate preserves the budget. Rate changes or a paused
to running transition establish a new window.

Receipt initialization into independent routing bypasses the whole-receipt
ordering gate, which admits no invocations; each resulting recipient enforces
its own ordering gate. This lets unrelated siblings reach independent routing
while one lane is paused.

Pending claim selection skips paused or throttled consumers. Admission rechecks
controls under the lock for races with pause. A raced claim is returned to
pending without charging its routing attempt count. Whole-receipt routing
continues through other recipients and records a pending control wait for the
held consumer. Ordering and deduplication use the existing immutable snapshots.

## Observation and retention

The control endpoint exposes pending/processing counts and the oldest pending
acceptance age. Backlog waiting reasons are `subscription_paused` and
`subscription_rate_limited`, computed from live state before filtering and
pagination. Active processing and receipt leases take precedence; controls then
precede ordering, recorded capacity, and retry backoff.

Pause introduces no automatic event expiry. Pending events remain subject to
account retained-event and byte limits; publication is rejected when storage is
full. Settled receipt and invocation retention remain governed by existing
contracts. Controls cover captured application and application backfill routing;
workflow, object notification, and pre-snapshot dynamic routing paths are outside
this contract.

# ADR-1271: Read-only evidence diagnosis in the retry queue

## Status

Accepted, 2026-10-13.

## Context

Evidence alerts link to diagnosis and history preserves repairs and transitions.
The queue still exposes worker reasons and historical receipts without identifying
which proof can be repaired at the time of a queue read.

## Decision

Enrich each returned retry queue entry with optional `evidence`: the existing
exact-generation diagnosis, diagnosis URL and matching inspection CLI command.
Pending actions inspect the frozen parent item; applied unresolved actions inspect
their child. Parent proof also requires its exact live generation to remain
retained. Missing or superseded live parents remain nonrepairable even when an
older result is saved. Diagnosis uses the queue observation timestamp.

Expose optional `wait_category` and `wait_reason` separately. Pending current
policy pause/disable overrides identify operational control; otherwise categorize
the last worker evaluation (or saved action reason) as policy, budget, cooldown,
evidence, execution or worker. These reasons may be historical. Fresh evidence
can be repairable while a pause or budget wait still prevents submission. Neither
field evaluates all admission gates or asserts that the retry is ready to run.

Perform enrichment only for the existing bounded returned page, after counts and
truncation are computed. Reuse existing evidence readers under the memory store
lock or the PostgreSQL read-only repeatable-read queue transaction. Share the
existing request timeout and check cancellation between entries. No public
per-entry call, write transaction, worker tick or notification capture occurs.
Internal queue aggregation used by workers does not perform enrichment.

Missing/expired review authority and changed plans report an explicit
`evidence_unavailable_reason`; database errors fail the read. Initial recoveries
retain their separate evidence routes and omit the retry evidence object. Their
operational wait category is still displayed. Queue counters, budgets, waiting
ages, worker evaluations, notification and repair receipts, and durable history
remain unchanged by reads.

CLI queue output prints the operational wait, exact evidence generation, current
diagnosis, repairability and inspection command/link. The command contains no
repair invocation or invented audit request. OpenAPI and SDK models add optional
fields for backward compatibility. Existing authorization and MFA remain in
force. No route registration, migration or new limit is required.

## Consequences

Operators can identify repairable parent or child proof directly from the queue
without confusing it with a saved wait reason. Diagnosis is a snapshot; repair
and the normal worker recheck their own authority. Bounded enrichment adds read
queries for returned retry entries and never changes execution state.

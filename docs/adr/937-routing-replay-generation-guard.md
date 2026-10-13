# ADR-937: Routing replay generation guard

Date: 2026-10-11
Status: Accepted; production qualification pending
Related: ADR-936

## Context

An acceptance can survive multiple routing recovery attempts. A reviewed action
must not requeue a later failed generation after another operator has already
recovered the generation that was reviewed.

## Decision

Selective routing replay accepts optional nonnegative int64
`expected_generation`, requiring `expected_accepted_at`. PostgreSQL locks the
authorized original acceptance and then its recipient, checks the exact acceptance,
routing generation and failed state, and requeues in the same transaction.
A generation mismatch returns HTTP 409 with code
`event_replay_generation_changed` before history, age override or routing mutation.
Memory storage performs the same checks under its existing mutex.

Original acceptance recovery offers carry the recipient-owned generation when
available. Commit recovery planners validate it against receipt evidence. Apply
compares the reviewed generation with fresh recipient evidence, including when
the fresh recipient no longer offers recovery. Stale actions are skipped as
`generation_changed`; the same result applies if the server catches a race after
the fresh read. Missing generation evidence is skipped as
`generation_guard_unavailable`. Legacy receipts and historical backfills do not
acquire an invented generation from replay history.

Routing idempotency keys use action kind/URL, source/event ID, subscriber,
acceptance instant and routing generation. Observation time is no longer part
of the routing key. Execution recovery keys retain their existing contract.
Ambiguous writes still stop the batch for inspection without automatic retry.
Omitting the new field preserves existing acceptance-only and unguarded API
behavior. Generation-guarded requests never fall back to weaker storage support.

## Consequences

Concurrent guarded recoveries of one failed recipient generation cannot both
requeue it. Each consumer keeps its own independent recovery generation.
New plans must be reviewed when generation evidence changes; older plans with
no generation cannot be applied automatically. PostgreSQL concurrency and
production failure scenarios still need qualification; builds and static
checks do not establish that qualification.

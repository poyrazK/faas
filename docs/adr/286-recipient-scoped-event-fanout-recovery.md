# ADR-286: Recipient-scoped event fanout recovery

- Status: Accepted
- Date: 2026-09-28
- Amends: ADR-284, ADR-285

## Context

ADR-285 freezes each new event's eligible subscription candidates. The outbox
still retried the whole event receipt when any one candidate failed to enqueue.
Successful candidates were visited again on every retry, and a permanently
invalid candidate could keep the receipt pending without a terminal outcome.

## Decision

Persist routing progress by subscription ID on the outbox receipt. Each
candidate reaches one of four states: `pending`, `filtered`, `enqueued`, or
`failed`. The scheduler records each outcome while holding the receipt claim.
It skips terminal outcomes on recovery, so a crash after one recipient was
enqueued can safely retry remaining candidates; deterministic invocation IDs
also cover a crash between enqueue and progress persistence.

Filter mismatches are complete outcomes. Invalid filters and missing target
apps fail permanently. Other enqueue errors retry with the existing outbox
backoff and become terminal after 12 candidate attempts. The receipt is
acknowledged when every candidate is filtered, enqueued, or failed. The
recipient's final error is retained on the outbox row and emitted in a
structured scheduler error log. Invocation execution retries and dead-letter
handling remain independent after enqueue.

Legacy receipts without a snapshot continue to use the ADR-284 routing path.

## Consequences

A transient or poison candidate no longer causes successful candidate work to
be repeated after progress is saved, and poison candidates cannot retry forever.
The outbox retains terminal routing errors for operator inspection. A recipient
that reaches the routing retry cap is terminally failed; replay tooling for
those pre-invocation failures can be added separately.

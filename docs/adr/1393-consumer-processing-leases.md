# ADR-1393 · ADR: Application-owned consumer processing leases

- Status: Accepted
- Date: 2026-10-15

## Context

Scheduler invocation leases fence platform execution, but applications need to
coordinate expensive preparation across at-least-once duplicate deliveries.
The existing consumer inbox already commits business writes and receipts atomically.

## Decision

Add an opt-in Go SDK processing lease stored in the application's PostgreSQL
database alongside its inbox. Claims use the inbox's acceptance/consumer identity
and advisory lock, immutable input digest, database-clock expiry and a random
256-bit token. Renewals reject expired or replaced tokens. Completion validates
and removes the claim in the same transaction as business writes and receipt.
Replays use the existing receipt without repeating the callback.

## Consequences

Consumers recover independently without a new platform lifecycle or migration.
Preparation must be side-effect-free, completion must be short, and existing
HTTP success remains the delivery acknowledgment. Detached acknowledgments and
platform visibility need a separate end-to-end contract. See
[the processing contract](../event-consumer-processing.md) for failure handling,
retention, concurrency and adoption constraints.

## Validation

Build the Go SDK and run static lint without tests. Local tests are not added or
run under the user's standing instruction; database concurrency behavior has not
been exercised locally.

# ADR: Node deferred event acknowledgment worker

- Status: Accepted
- Date: 2026-10-17

## Context

Node already has application-owned transactional consumer receipts and generated
deferred acknowledgment APIs. Applications need to combine their independent
database and platform commits while retaining consumer-scoped deduplication.

## Decision

Add an explicit worker capability constructed from authenticated delivery context.
Reuse the existing receipt transaction and platform routes. Require a confirmed
renewal before new business work; renew serially, abort cooperatively on loss or
uncertainty, and stop/join renewal before a terminal callback. Complete only from
a confirmed receipt. Receipt-only recovery never invokes business code; negative
decisions require stopped work, known rollback and exclusive job ownership.

Send saved completion JSON as an application/json Blob through the configured
generated transport, avoiding number/text changes from a parse/stringify round
trip. Validate and copy bounded response metadata; sanitize callback errors and
redact capabilities. Fail closed when a replay generation cannot be represented
exactly by the generated numeric Node status model.

Add optional cooperative cancellation to the existing Node receipt transaction,
without racing an active driver call or treating cancellation after COMMIT as
rollback. Keep durable job storage, process lifetime, driver timeouts, retention
and external-effect idempotency application-owned. The integration example
persists a reconciliation phase before business execution.

## Consequences

No platform schema or API changes are needed. Delivery remains at least once;
database receipts provide the business-write deduplication boundary. There is no
new durable acknowledgment-intent store or Node preparation lease in this change.
See the [worker guide](../event-acknowledgment-node-worker.md).

## Validation

Use Node SDK generation/build and static documentation, packaging and export
checks. Local tests and new test/assertion changes are excluded by the standing
instruction. Hosted CI is required before release; database concurrency and
restart behavior are not claimed as locally verified.

# ADR: Python durable acknowledgment intents and claimed callback recovery

- Status: Accepted
- Date: 2026-10-17

## Context

The Python async worker commits receipts and sends callbacks, with the ingestion
job retaining callback responsibility until confirmation. Go and Node support
durable intents that transfer this responsibility with business writes. Python
needs the same crash/restart boundary without a platform schema or API change.

## Decision

Package the canonical Go intent DDL unchanged in Python, preserving it through
SDK generation and wheel/sdist builds. Installation and access control remain
explicit application responsibilities.

Add `run_durable` and receipt-only `save_completion_intent`. A private optional
pre-commit hook stages completion for both new and replayed receipts. Preserve
public receipt signatures, digest framing, immediate worker behavior and platform
admission/renewal rules. Conflicts roll back business writes with receipt/intent.
Staging sends no terminal callback. Unknown COMMIT remains unknown, closes the
connection and returns no confirmed intent result.

Use the existing immutable intent/supersession protocol. Repeated stages retain
claims/counters/due times. Newer generation/attempt pairs supersede unfinished
older rows and erase their secrets; stale stages fail. Preserve confirmed history.
Native Python integers carry full int64 generation fences without rounding.

Add one-row recovery using database time, SKIP LOCKED, opaque 32-byte claim tokens
and conditional settlement. Verify stored hashes and replay the original callback
through the configured client and the live worker's bounded HTTP protocol.
Subtract elapsed local time from database lease evidence before HTTP. Never renew
platform leases or run business code in recovery. Recognize Go/Node negative
actions and recheck for receipts; Python stages completion intents in this increment.

Share cooperative cancellation/owned cleanup and terminal callback validation.
Do not force-cancel SQL/business work. Preserve confirmed commits after late
cancellation, sanitize intent driver errors, redact recovery capabilities and
reject their serialization. Expose bounded immutable metadata diagnostics.

## Consequences

An ingestion job can finish after confirmed receipt/intent COMMIT. A separately
available recovery host owns prompt callbacks and scheduling. Version-2
`python-durable` example records remain distinct from version-1
`python-immediate` records; no relabeling or automatic mode switching occurs.

Applications own installation, ingress authentication, exclusive ownership,
driver timeouts, protected secrets/backups, polling and retention. Database/HTTP
are not atomic together. Successful callbacks with unknown settlement require
unchanged-input retries and server idempotency. Deleting newest metadata loses
the local supersession fence. External effects still need an outbox or their
own idempotency protocol. No platform migration, route, registry, ordering or
external exactly-once change is needed.

## Validation

Use compilation/lint, isolated SDK generation parity without test files, canonical
DDL parity, wheel/sdist inclusion and documentation/static checks. Do not add or
run local tests or modify assertions. Hosted PostgreSQL contention, unknown COMMIT,
callback, restart, cancellation and cross-SDK acceptance remains pending before
release. See the [Python intent contract](../event-acknowledgment-python-intents.md).

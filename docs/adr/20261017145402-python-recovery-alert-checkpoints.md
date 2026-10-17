# ADR: Restart-safe Python acknowledgment recovery alerts

- Status: Accepted
- Date: 2026-10-17

## Context

The ordinary Python recovery watch intentionally keeps alert state in memory.
Restart loses unresolved identity and pending notifications. Node already supports
application-persisted metadata checkpoints with exact acknowledgment and fresh
confirmation after resume. Python needs the same durable boundary without changing
its ordinary watch or callback delivery tables.

## Decision

Add an opt-in checkpointed watch and pure validation/exact-acknowledgment helpers.
Use a distinct Python version-1 kind, canonical UTC microsecond timestamps,
normalized owner scope and complete effective policy. Reconstruct bounded known
fields, validate incident/sequence lineage and reject foreign formats. Validation
is not authentication or protection against older valid snapshots.

Retain session/sequence, timestamp high-water mark, unresolved IDs, historical
values and pending/acknowledged lifecycle metadata. Resume resets conditions to
unknown and discards all unfinished monotonic proof. Fresh complete advancing
observations must sustain opening, reconfirmation and resolution. A renewed breach
reconfirms the same unresolved identity; missing evidence never erases it.

Only opened/resolved lifecycle notifications enter the outbox. Reuse the bound of
1024 pending-plus-reserved entries and 1024 acknowledgment receipts. Reserve queue
and safe sequence room for each incident's future resolution. Capacity exhaustion
blocks openings without dropping pending entries or replacing IDs. Exact batches
are all-or-nothing, idempotent against retained receipts, and enforce opening
acknowledgment before/together with resolution.

Require an awaited on-progress callback. Publish an initial detached snapshot
before health reads, permitting pending replay during database outage. Applications
must atomically persist the complete checkpoint before any notification attempt
and fence one serialized writer. Optional acknowledgment polling uses the same
pure helper between observations. Reuse ordinary scoped reads and cooperative
cleanup; do not race/abandon active database or callback work.

Callback errors expose a fixed phase/code and detached result without arbitrary
causes. Python cancellation remains a CancelledError subtype carrying a detached
interrupted result for independently bounded fenced shutdown persistence. Emit no
automatic resolution/acknowledgment on shutdown. Preserve ordinary watch behavior.

## Consequences

Applications can resume stable incidents and replay pending metadata notifications
with at-least-once delivery and downstream deduplication. Persistence, available
monitoring hosts, incident reconciliation, transport and fencing remain application
responsibilities. A failed save may already have committed and requires reading
current fenced state. No platform schema, route, registry, notification transport
or external exactly-once guarantee is added.

## Validation

Use compilation/lint, Python 3.11 syntax, static protocol review, isolated SDK
generation without test files, wheel/sdist inclusion, schema and documentation
checks. No tests/assertions are added/changed and no local tests run. Hosted
restart, replay, storage fencing, capacity, timing and cancellation qualification
remains pending before release. See the
[Python checkpoint contract](../event-acknowledgment-python-checkpoints.md).

# ADR: Durable Node acknowledgment intents

- Status: Accepted
- Date: 2026-10-17

## Context

The Node worker can commit consumer business writes and then acknowledge delivery,
but a process can fail between those commits. Applications need durable callback
recovery without repeating business code or losing consumer-scoped deduplication.
The Go SDK already defines an application-owned intent schema and recovery protocol.

## Decision

Reuse that schema byte-for-byte, packaged into Node by the existing receipt
contract generator. Add an internal pre-commit hook to the receipt transaction.
`runDurable` saves completion with business writes and the consumer receipt,
including receipt replay. `saveCompletionIntent` stages a committed receipt without
business code. Explicit retry/dead-letter staging uses the consumer identity lock
and rejects committed receipts; exceptions do not choose negative decisions.

Bind immutable intent inputs and serialize staging by owner/invocation. Order
lineage by generation then attempt, supersede unfinished older intents and reject
stale stages. Repeated identical staging preserves claims, counters and schedule.

Recovery uses database-time, opaque, expiring claims with SKIP LOCKED. Validate
saved capability/action digests, send only the original callback, bound HTTP by
remaining claim time and fence settlement by claim token/state/expiry. Recheck
receipts before negative callbacks. Keep process lifetime, ingestion jobs,
polling, ownership, database timeouts and retention application-owned. Do not mix
immediate and durable modes for an attempt.

Share existing Node callback validation/retry logic between live and recovery
workers. Preserve exact JSON text and redact capabilities and SQL diagnostics.
Return committed local recovery status for callback outcomes; uncertainty is
`unconfirmed`, not a successful platform acknowledgment. Throw on storage/claim/
settlement failures. Use decimal int64 diagnostic generations, while rejecting
unsafe numeric generations for Node callback recovery.

## Consequences

No platform migration, registry or API route is added. Non-durable helpers do not
consult the optional table. Business receipts remain the transactional deduplication
boundary. There is no transaction across HTTP and PostgreSQL, no implicit worker
service and no exactly-once guarantee for external effects. Terminal rows clear
raw callback data, with backup/in-memory protection and retention still owned by
the application. The version 2 example explicitly selects durable mode and cannot
silently reinterpret old immediate-mode jobs.

See the [Node intent guide](../event-acknowledgment-node-intents.md).

## Validation

Node SDK build, contract and API generation, package/export inclusion and static
documentation checks are allowed locally. No new tests, assertion changes or local
test execution are part of this increment. Hosted runtime acceptance is needed for
crash, concurrency, lease-loss and shared Go/Node database behavior before release.

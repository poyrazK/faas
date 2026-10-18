# ADR: Node acknowledgment retry-policy adoption parity

- Status: Accepted
- Date: 2026-10-17

## Context

Go can adopt existing unbound intents with timestamp/count fences and durable
command receipts. Node recovery honors the resulting bindings, but Node cannot
yet issue or resolve adoption commands. Existing Node diagnostic timestamps use
JavaScript Date precision, which loses PostgreSQL microseconds needed by fences.

## Decision

Add an explicit candidate read and adoption API in Node. The read formats bounded
timestamps as text in SQL and preserves microseconds without changing ordinary
diagnostic/staging timestamps. Canonicalize request times to UTC RFC3339 with
fractional trailing zeros omitted. Use the Go owner/command and invocation lock
namespaces, v1 JSON request digest, guards, mutation and receipt table unchanged.
Package the byte-identical optional DDL through the shared schema generator.

Copy and validate options/policy before I/O, excluding configuration getters and
unknown keys. Diagnostic candidate generations remain decimal int64 strings;
adoption rejects values above Node's safe-integer range before database access.
Write Go-compatible numeric JSON generation fields only inside that bound.
Preserve exact timestamp fields and snake-case status JSON in retained snapshots.

Use one dedicated connection and short READ COMMITTED transaction. Replay matching
receipts before reading current intent state and roll back read/replay transactions
without another COMMIT. Save adoption or conflict/not-found outcomes atomically;
return frozen receipts, or throw a typed guard error with its confirmed receipt.
A changed request under a retained command ID conflicts without a receipt.
Malformed storage fails closed with sanitized diagnostics.

Once COMMIT starts, late cancellation cannot erase a confirmed result or establish
rollback. Unknown COMMIT returns the existing commit-unknown type/code with no
receipt and discards the connection. Join rollback/release; secondary cleanup
failures do not override an unknown commit. Host contexts and driver timeouts bound
operations; no HTTP, business callback, claim or platform renewal is added.

## Consequences

Go and Node can resolve each other's commands within Node's supported generation
range. Applications retain the exact original command and receipts, authorize the
owner scope, install the optional schema and upgrade every recovery host before
enabling bindings. Storage/cleanup errors may occur after effects; retry the
original command to recover its retained outcome. Python adoption remains a
follow-up. Revision, removal and automatic disposition remain separate work.
See the [shared adoption contract](../event-acknowledgment-policy-adoption.md).

## Validation

Node generation (1255 postprocessed files) and the final TypeScript/declaration
build passed, with no generated API drift. SQLC statically compiled all seven
adoption/candidate statements against fresh and upgraded schemas without a
database. Static source comparisons confirmed shared DDL bytes and Go request
field order/domain. Ten packaged module/declaration/schema/export files matched
the final build. Shared schema parity, example syntax, Python 3.11 generator
syntax, ADR uniqueness and docs route checks passed. Existing staging, recovery,
callback and settlement implementations are unchanged.
No local tests or SDK helper smoke checks are run. Hosted behavioral qualification
remains pending for cross-SDK replay, timestamp fences, concurrent claims/staging,
rejected outcomes, cancellation, response loss and cleanup failures.

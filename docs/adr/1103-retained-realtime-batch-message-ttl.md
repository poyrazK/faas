# 1103: Default and per-message TTL for retained channel batches

## Decision

Extend retained channel batch publishing with optional batch `ttl_seconds`
(default zero) and nullable item overrides. Omitted/null item TTL inherits the
batch default; explicit zero disables delivery expiry for that item. Positive
values are bounded by the existing 86400-second protocol limit. Validate the
batch default and every resolved TTL before any state write or fan-out.

Resolve defaults at the HTTP boundary. State batch items carry effective integer
TTLs and independently validate them for direct store callers. Include positive
effective TTLs in the batch's canonical idempotency hash; omit zero TTL fields to
preserve hashes for existing non-expiring batches. Equivalent configurations with
the same effective item lifetimes have the same hash. A changed effective TTL
conflicts with the retained batch ID, while identical retries reconstruct original
message deadlines from the ledger's original creation timestamp.

Calculate each deadline from one batch creation timestamp, and persist its
companion expiration row inside the existing atomic batch transaction. Use
PostgreSQL microsecond precision for the creation snapshot so reconstruction
matches persisted deadlines. MemStore creates matching expiration-bearing message
records under its existing lock. No new migration is needed beyond ADR 1102's
expiration companion table. Carry deadlines into legacy fan-out so retries cannot
re-deliver expired payloads through live queues. Retained WebSocket, SSE and REST
use the existing expiration checkpoint behavior without decoder changes.

Expiry does not change retention, sequence allocation, reducers or existing batch
ledger lifetime. A TTL may expire before a long transaction completes; it remains
a delivery deadline from the original persisted creation time. Item overrides are
represented by a batch-specific Go DTO so zero can be distinguished from omission.
Python and Node generated publishing models expose default and override fields.

## Validation

Go package compilation, TypeScript type checking, Python lint/compilation/AST,
OpenAPI bounds and serialization-source checks, and whitespace checks. Runtime
batch rollback, expiry and retry checks remain deferred at the user's request.
ADR 1102's migration remains unapplied in this session; deploy it before enabling
batch TTL along with updated servers and checkpoint-capable SDK clients.

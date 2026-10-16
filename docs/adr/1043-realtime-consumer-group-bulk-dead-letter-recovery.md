# ADR 1043: Bounded bulk dead-letter recovery

Status: accepted for preview implementation; runtime validation pending.

## Decision

Add account/MFA POST routes beneath each group's `dead-letters` resource:
`preview` uses read scope; `replay` uses deploy-write scope. These reuse ownership,
preview/plan checks, no-store responses and existing group-row locking. Scoped
worker HTTP routes remain unchanged. Store methods enforce existing dead_letters
and replay grants when called with a worker context.

A read-only preview filters materialized dead snapshots by up to 32 exact unique
failure codes, exclusive `after_sequence`, optional inclusive `through_sequence`,
and limit 1–32 (default 32). Return ascending sequence, attempt count, failure code,
current ordering blocker, nominal retention expiry, and exact `dead_since` timestamp,
plus immutable group UUID and `has_more`. Empty code lists select all codes.
Keyset pagination uses the last returned sequence; no snapshot is held between
pages. Expired exhausted leases are excluded until existing claim/replay maintenance
materializes them as dead. Preview never mutates leases or reserves recovery time.

Bulk replay accepts 1–32 unique explicitly selected sequences, their exact preview
`dead_since` timestamps and expected group UUID. Preserve timestamp precision when
copying selections; JavaScript Date truncates PostgreSQL microseconds. Node generated
models keep timestamps as strings. Python datetime supports microsecond precision.
No broad filter is reevaluated during replay. A UUID mismatch conflicts the entire
request, protecting recreated groups; a changed dead timestamp yields `stale`,
protecting an intervening replay/failure cycle. The selection is a concurrency check,
not a signed authorization token; ordinary ownership and scope checks still apply.

Under one group lock, expire old leases through existing recovery maintenance,
then process selections in request order. Return `replayed`, `ordering_blocked`,
`stale`, `not_dead` or `missing` for each. Missing does not prove retention expiry:
cleanup and other removals cannot be distinguished without a deletion ledger.
Expected per-item conflicts allow other items to replay; unexpected errors roll
back all changes, including maintenance. Only a committed batch returns successful
outcomes. Do not automatically retry an uncertain transport response.

Replayed snapshots reset attempts/failure state and become ready now. Existing
retention triggers clear dead/warning timestamps. Preserve pause/drain, group
assignment and completion counters, ordering identity, and fencing counter. An
unexpired same-key lease blocks an ordered replay. Multiple selected dead items
on a key may become ready together; existing claim ordering serializes them. Under
skip policy, later work may already have completed, so replay is explicit recovery,
not restored historical ordering. A new claim allocates a fresh fencing token;
external effects still require application idempotency and storage enforcement.

## Implementation and rollout

New SQL queries use generated sqlc methods. An isolated compile-time table
projection mirrors the migration-owned columns needed for recovery; it is never
applied to a database. Register its generation in the root sqlc configuration.
No new migration or persistent preview record is needed. This feature depends on
the existing group, ordering, retention and fencing migrations. Preview/replay
request bodies are bounded at 8192 bytes, selections at 32 items. Audit the group
UUID, selected count and replayed count without message payloads or credentials.

OpenAPI and generated Node/Python SDKs expose both routes and typed outcomes.
Go/Node builds, generation/static contract checks are used for this change.
Runtime tests, database migration execution and dashboard rendering are deferred
at the user's request.

# ADR: Python processing recovery inspection

- Status: Accepted
- Date: 2026-10-17

## Context

Python now supports local processing leases and coordinated durable preparation.
Operators need to diagnose busy/interrupted jobs without granting ownership,
selecting protected rows into diagnostics or performing recovery mutations. Node
already defines a single-snapshot inspection protocol.

## Decision

Add `ainspect_event_consumer_processing` and frozen
`EventConsumerProcessingInspection` using the existing authenticated request
constructor/digests and application-owned receipt/processing tables. Port Node's
single materialized-clock MVCC statement verbatim except positional bindings.
Select equality booleans and bounded UTC timestamps only, never tokens, digests,
source/event IDs, bodies or saved results. Use no explicit transaction, advisory
or row lock, platform client/callback, mutation, enumeration or automatic schema install.

Validate exact row shape, native booleans/null evidence and canonical microsecond
UTC timestamps. Use integer timestamp arithmetic. Conflict takes precedence over
matching receipt completion, then active/expired claim, then absence. Matching
claims retain timing even alongside receipts; conflicting claims expose none.
Python absent optional fields are `None`; shared classifications match Node.

Use an exclusively leased idle autocommit psycopg async connection and existing
cooperative cancellation ownership. Join active SQL/cursor cleanup, sanitize
storage/decoder failures and close failed query/cursor connections. No commit is
sent and no unknown-COMMIT state is introduced. Keep processing/worker/recovery
protocols unchanged. Add read-only example helpers for explicit version-4 and
version-5 jobs without acknowledging, changing phases or relabeling records.

## Consequences

No schema, platform endpoint, migration, registry or service changes are needed.
Applications own authorization, exact protected input, authoritative database
access, bounded driver/acquisition/cleanup calls, retention and explicit recovery
decisions. Matching receipt metadata does not validate saved JSON; existing
receipt reconciliation remains necessary. Absence/expiry never proves rollback,
stopped work or ownership, and concurrent changes can stale the snapshot
immediately. See the [Python inspection contract](../event-consumer-processing-inspection.md#python-inspection).

## Validation

Use compilation/lint, Python 3.11 syntax, SQL/parameter source parity, isolated SDK
generation, wheel/sdist inclusion, schema/ADR/documentation and static checks.
Add no tests, change no assertions and run no local tests. Hosted concurrency,
conflict precedence, timestamp boundaries, cancellation and metadata-only query
qualification remains pending before release.

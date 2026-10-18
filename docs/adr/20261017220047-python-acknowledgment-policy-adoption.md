# ADR: Python acknowledgment retry-policy adoption parity

- Status: Accepted
- Date: 2026-10-17

## Context

Go and Node can bind existing unbound intent backlog using timestamp/count guards
and durable command receipts. Python recovery honors those bindings, and its
existing intent listing preserves microseconds and full int64 generations. Add
Python adoption with the same request identity and retained outcomes.

## Decision

Add frozen slotted adoption options and receipts, a typed guard error with optional
confirmed receipt, and `aadopt_event_acknowledgment_intent_retry_policy`. Copy and
validate options/policy before I/O, accepting native integers across the full
int64 generation range and preserving microsecond timestamp strings. Canonicalize
times to UTC RFC3339 with fractional trailing zeros omitted. Reuse the Go/Node v1
digest framing, command/invocation advisory lock namespaces, mutation guards and
payload-free snake-case receipt snapshot format.

Use the existing shielded owned-operation boundary with an exclusively leased,
idle autocommit async psycopg connection. Own a short explicit READ COMMITTED
transaction. Close the cursor before COMMIT. New adoption/conflict/not-found
outcomes commit atomically; matching receipt replay rolls back without sending
another COMMIT. Keep the original action, protected inputs, count, due time, state
and expired claim metadata; only policy columns and timestamp change.

Cancellation checks precede queries and COMMIT. Once COMMIT starts, preserve
confirmed evidence or the existing unknown-commit exception. Join rollback and
close a connection with unknown COMMIT, failed rollback or sanitized store failure.
Secondary cleanup errors cannot replace unknown COMMIT. Driver errors and invalid
stored metadata are sanitized. No HTTP, business handler, claim or renewal occurs.

Package the byte-identical optional receipt DDL through the shared schema generator,
register both wrapper modules and SQL for Python regeneration, and export the API
and schema from the package root and wheel/sdist.

## Consequences

All SDKs support explicit adoption and share retained command outcomes. Node keeps
its safe-integer generation restriction; Go/Python retain full int64 generations.
Applications own authorization, schema installation, bounded driver operations,
saved command inputs and receipt retention. Existing listing supplies Python's
fence; no additional candidate API or table upgrade is needed. Revision/removal,
pause/resume and automatic disposition remain separate work. See the
[adoption contract](../event-acknowledgment-policy-adoption.md).

## Validation

Isolated regeneration without tests produced 6520 byte-identical source/schema/type
marker files. Python 3.11 syntax and Ruff passed. Seven adoption/schema/export
files matched current bytes in both wheel and sdist. SQLC statically compiled all
six adoption SQL calls against fresh and upgraded application schemas, without a
database. Shared DDL bytes, Go digest field order/domain, schema generation parity,
ADR uniqueness and docs route checks passed. Existing staging, claim, callback,
settlement and cancellation-boundary implementations are unchanged.
No local tests or SDK helper smoke checks are run. Hosted behavioral qualification
remains pending for cross-SDK replay, full int64 generations, timestamp/count
guards, concurrent claims/staging, rejected outcomes, cancellation and unknown
COMMIT/cleanup failures.

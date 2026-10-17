# ADR: Python consumer processing leases

- Status: Accepted
- Date: 2026-10-17

## Context

Python receipts deduplicate transactional business writes and durable intents
recover callbacks. Side-effect-free preparation can outlive a short database
transaction and needs a commit fence against expired/replaced workers. Go and
Node already define a shared application-owned processing protocol.

## Decision

Package canonical Go processing DDL byte-for-byte in Python. Add async claim,
renew and completion capabilities using existing consumer identity/input digests,
random 32-byte tokens and database-clock expiry. Serialize claim with the consumer
advisory lock, recognize matching receipts first, and replace only expired claims
without changing their immutable input. Positive whole-millisecond durations must
fit Go time.Duration.

Add internal optional transaction hooks to check/lock the claim before new business
work and conditionally delete it after receipt insertion. The delete is the local
completion linearization point; optional intent staging follows before the same
COMMIT. Receipt replay validates scope/input/JSON without requiring a live claim.
Preserve public unleased transaction signatures and optional-table behavior.

Compose independently acquired matching capabilities with immediate/durable
Python workers. Receipt-only probes skip the processing fence; new work retains
existing platform confirmation/renewal. Bind capabilities to one event loop,
serialize per-object operations through completion, reject copying/pickling,
redact formatting and sanitize storage failures. Preserve application callback
exceptions and existing conflict/intent error categories.

Use existing cooperative cancellation ownership: shield/join active SQL/cleanup,
never treat interrupted COMMIT as rollback, and discard unknown-commit connections.
Unknown claim COMMIT returns no capability. Retain confirmed receipt progress where
available. Select leased processing with explicit version-4 example jobs without
reinterpreting older immediate, durable or negative-decision records.

## Consequences

No platform schema, route, registry, migration or service changes are required.
Applications own schema installation, side-effect-free preparation, local renewal
cadence, platform renewal/budget during preparation, driver deadlines, protected
fenced jobs and retention. Stop/join local renewal before the short transaction.
Expiry fences commits but cannot stop physical work. External effects still need
idempotency or an outbox. A dual-lease preparation coordinator is deferred.
See the [Python protocol](../event-consumer-processing-python.md).

## Validation

Use compilation/lint and Python 3.11 syntax, isolated SDK generation, schema source
parity, wheel/sdist inclusion and static protocol/documentation checks. Do not add
tests, change assertions or run local tests. Hosted concurrency, takeover, rollback,
expiry, callback and cancellation acceptance remains pending before release,
including shared Go/Node/Python processing state.

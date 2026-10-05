# ADR-593 · Durable managed PostgreSQL compute resizing

- **Status:** accepted for operator preview; live provider qualification required
- **Date:** 2026-10-05
- **Decision:** resize an existing database's service class through a persisted,
  generation-fenced intent and the existing lifecycle reconciler.

## Contract

`POST /v1/postgres/databases/{id}/resize` accepts a caller-generated UUID
`request_id` and a target Gregale `service_class`. It commits the intent before
provider I/O and returns its status with HTTP 202. Reusing a request UUID with
the same database and class returns that operation; changing either conflicts.
`GET /v1/postgres/databases/{id}/resizes/{request_id}` reads progress. API, CLI,
and SDKs expose expected connection interruption; no zero-downtime promise is
made. Class mappings stay inside the provider adapter.

The catalogue keeps its last confirmed specification while `updating`. An
immutable operation captures that specification, backend fingerprint, lifecycle
identity, exact data identity and target generation. Completion atomically
publishes the observed target class, observed generation and operation success.
Bindings and credential identities are preserved. Other specification fields
cannot change in this increment. Ordinary databases with a recorded data
identity are eligible; environment-clone-owned targets retain their separate
proof protocol and cannot use this operation.

Admission checks the pinned backend's resize capability, customer plan and
usage guardrails. Database locking serializes reservation with deletion,
cutover, pending restores, clone snapshot/write-fence holds and unfinished
binding changes. An accepted resize remains recoverable after admission closes.
Another resize or deletion cannot supersede an unresolved resize.

Every attempt claims the existing database lease. The adapter first reads the
exact recorded dataset and its current compute configuration. If the requested
configuration is already complete, it adopts it without another mutation.
Pending provider work is polled; unexpected identity or configuration drift
blocks progress. A lost response retains the intent for observation and retry.
Errors never reset the generation or automatically roll back a mutation whose
outcome is uncertain. Operator diagnosis must establish the actual provider
state before intervening. This is convergence, not exactly-once external I/O.

For Neon, only the pinned branch's unique primary endpoint is updated. The
project's current default branch and project defaults cannot select another
dataset. The adapter changes only autoscaling minimum/maximum values, waits
for provider work to finish, and verifies the remaining specification. Resize
does not create a replacement endpoint, retrieve passwords or alter SQL roles.

## Qualification and validation

Qualification v5 binds resize declarations and requires a disposable SQL probe
that changes class, checks data and existing credentials, replays the same
request, and returns to the original class. Unsupported providers remain valid
with resizing disabled; old approvals cannot enable the enlarged contract.

Memory and PostgreSQL store tests cover atomic reservation, UUID replay and
conflicts, competing mutations, lease expiry, stale completion and restart
recovery. Adapter fault injection covers lost mutation responses, asynchronous
work, changed defaults, exact branch selection and configuration drift. API and
SDK tests verify authorization, plan limits, safe output and progress polling.
Live Neon qualification remains a rollout requirement.

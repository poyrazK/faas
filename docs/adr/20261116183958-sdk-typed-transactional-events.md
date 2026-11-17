# ADR: Typed transactional event publication across SDKs

Status: Accepted (SDK preview)

## Context

Typed event-triggered pipelines coordinate consumption, but applications also
need to commit business writes and publication intent together. The Go preview
SDK already has an application-owned PostgreSQL outbox, including durable retry
policies, publishing pauses and a shared rate bucket. Independent Node/Python
implementations must preserve its protocol and cannot bypass those controls.

## Decision

Add opt-in typed publishers, managed PostgreSQL transactions and bounded recovery
passes to the Node/Python preview entries. Reuse EventContract validation and
native expected-app-ID guarded publication and read-only verification. Share the
existing Go application tables; generate both SDKs' DDL/SQL modules from checked-in
SQL and Go DDL. No platform endpoint, quota, scheduler or broker changes.

Keep immutable original request bytes, key, app slug and retry deadline. Stage
inside the business transaction; poison that transaction on any staging error,
even one the callback catches. Never retry callbacks. Dispatch only after a
claim COMMIT is acknowledged. Propagate uncertain commits, fence expired and
replaced tokens, and bound HTTP by total time, lease and receipt retention.
Honor Go persisted policies, advisory publishing locks, pause state and rate
credit using database time. Status/progress contain no payload or capability.

Node uses the pg structural pool interface. Python provides an optional psycopg
factory adapter without importing a driver, requiring fresh idle connections.
Transaction handles expire with their owning operation. Applications authorize
business writes and metadata access; native credentials authorize publication.

Environment is part of the immutable request; keys remain account/app-wide.
Add the missing Go wire field and reject unknown retained request fields during
recovery/reconciliation. Document upgrading all Go workers before staging
new environment-specific rows. Do not silently retarget a request to production.

Real PostgreSQL acceptance exposed an existing Go DDL bug: request_body's check
rejected NULL, although successful settlement erases that payload. Fix fresh DDL
and provide an explicit app-owned constraint migration. Include that migration
in the new exported Node/Python DDL; no helper automatically installs it.

Keep PostgreSQL CAS/refill timestamps as original microsecond strings. Compare
native evidence at nanosecond precision without rounding through JavaScript Date
or Python datetime. Read-only reconciliation binds the saved bytes and exact row
snapshot and never republishes, resumes or extends a retry window.

## Consequences

The APIs are additive and preview-only. Existing callers keep their signatures.
Applications apply DDL explicitly and retain identities, run bounded worker
passes and handle uncertain commits. Payload data remains sensitive until native
acceptance is confirmed. Publication success does not imply consumer completion.
Node/Python can work on the same rows as upgraded Go workers while preserving
controls; this slice does not expose Node/Python operator mutation APIs.

Shared Go/Node/Python wire/policy/jitter fixtures, transaction/deadline/cancellation
unit tests, real PostgreSQL mixed-worker acceptance and the complete reviewed CSV
flow qualify the SDK path. Native transports in local examples remain protocol
fixtures; deployed broker/scheduler acceptance is separate.

# ADR-585: Managed operation results with durable webhook effects

- Status: Accepted for implementation; promotion requires runtime qualification
- Date: 2026-10-03
- Related: ADR-387 (managed exclusive operations), ADR-589 (Commit customer routing), ADR-076 (outbound webhooks)

## Context

Managed operations fence completion and can write opaque effects, but HTTP
handlers currently return only a result. Calling the application outbox API
separately leaves a gap between accepting that result and queueing delivery.
Gregale already owns a signed webhook ledger, retry scheduler, attempt history,
destination validation, and receiver-side deduplication contract.

## Decision

Managed HTTP request handlers may return the version 1 `ManagedOperationResult`
JSON envelope, containing an arbitrary JSON result and named webhook effects.
The reserved `gregale_operation_result` field opts in; ordinary responses retain
their current behavior. Reject unknown or duplicate control fields, unsupported
versions, duplicate effect names, invalid IDs/types, and oversized responses.
Opaque internal effects retain their existing behavior. Ordinary workflow
steps, jobs, and deployment task output do not interpret this HTTP response
contract; `managed_operation: true` workflow HTTP steps use the same result
envelope as managed app requests under ADR-587.

Advertise `X-Gregale-Operation-Result-Version: 1` to the guest only when both
schedd and gatewayd-internal support this protocol. Schedd negotiates over the
authenticated internal dispatch envelope; public and persisted customer headers
cannot assert support. Handlers using effects must fail before business work
when this header is missing. This prevents an older scheduler from interpreting
an effect envelope as an ordinary completed result during rolling upgrades.
Allow 4 KiB of internal response-envelope overhead around the 1 MiB handler body.

An effect references a registered webhook UUID, never an arbitrary target URL
or signing secret. Every receiver must explicitly include `operation.effect` in
its immutable tenant filter or app filter; an app wildcard is insufficient.
Account-scoped operations may use only their own app receiver. Customer-scoped
operations may use only a receiver owned by that exact authenticated tenant and
account, with an active linked surface for the operation's app. Account release
receivers and another app/customer/account's receivers are not destinations.

Under the existing account/operation/key/incarnation locks, insert the immutable
effect and its webhook delivery together with completion of the operation. Use
the same UUID as effect ID and delivery ID. Validate ownership again after
destination locks, using PostgreSQL's clock, so waiting out a lease cannot publish
an effect. Any invalid destination, expired owner, or insertion failure rolls
back the result and all effects. Deterministic validation failures fail the
operation; transient commit errors follow its existing bounded retry policy.

The webhook event is `operation.effect`. Correlate its immutable effect UUID
before enforcing this guard; existing application outbox events with that same
custom type retain their behavior. Its data contains the platform-owned
operation, app, optional tenant, generation, effect name, business event type,
and business payload. The existing dispatcher signs each attempt and controls
retry, cooldown, leases, dead letters, and manual retry. Recheck the committed
effect identity, receiver opt-in, account/app availability, tenant status, and
linked surface before each attempt. Unknown scope state transmits nothing and
recovers through the existing delivery lease. Revocation stops future attempts;
it cannot recall an HTTP request already started.

Operation inspection includes effect IDs and current delivery status/attempts.
Keep immutable receiver/type references when subscription deletion or delivery
retention removes ledger rows; report `unavailable`, never a guessed outcome.
Legacy opaque effects report `recorded`. Operation completion means result and
deliveries were committed, not that a customer received them.

Limits stay in `pkg/api/limits.go`: 32 effects per completion, 64 KiB of JSON
payload per effect, 256 ASCII bytes for business event type, 1 MiB for the entire
handler response. Existing plan webhook quotas and retention apply.

## Consequences

Backend handlers can return business outcomes and delivery intent without
managing webhook signing, retries, or a second platform API call. Delivery is
at least once: receivers must deduplicate the stable delivery ID. The platform
transaction does not include customer PostgreSQL writes or arbitrary external
API calls. Handlers still need idempotent business writes keyed by the operation
ID when an attempt completes its database work but loses its response.

Roll out the migration, apid event vocabulary, schedulers, and internal gateways
before deploying handlers requiring the support header. Commit remains under its
existing internal rollout gate. Drain active operations and queued effects before
rolling binaries back; old dispatchers lack the new scope recheck. Preserve the
forward-only migration and immutable receipts. No production deployment or
native x86_64 runtime qualification is implied by local acceptance.

## Validation

Memory and PostgreSQL tests cover atomic rollback, current tenant/surface scope,
payload-independent isolation properties, concurrent duplicate completion,
retained receipt identity, stale generations, and lease expiration while blocked
on a receiver lock. Scheduler tests cover explicit envelope parsing, ordinary
responses, permanent invalid results, and completed operations staying complete
while signed webhook delivery retries with a stable ID. Existing dispatcher,
OpenAPI/SDK, CLI, and Commit admission regression checks remain required.

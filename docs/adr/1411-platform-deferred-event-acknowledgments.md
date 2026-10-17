# ADR-1411 · ADR: Platform-managed deferred application event acknowledgments

- Status: Accepted
- Date: 2026-10-15

## Context

Consumer-owned leases protect application database processing, but ordinary HTTP
success cannot distinguish durable background acceptance from completion.
Scheduler claims already fence attempts and replay generations.

## Decision

Capture an optional acknowledgment lease duration in each application recipient
snapshot. Before dispatch, persist a random token's digest in an invocation-owned
operational record; carry raw token bytes only in transient dispatch headers.
HTTP 202 hands off the lease after scheduler renewal stops. App-authorized
callbacks must also match the token and current execution claim.

Serialize handoff, callbacks and expiry on the invocation row. Completion before
handoff is permitted. Matching completion repeats are idempotent; changed results
conflict. Bound renewal to one hour per attempt. A bounded sweep retries with
captured backoff and finite attempt budget, independently for each consumer.

## Consequences

Existing invocation state, quota reservation, capacity and keyed ordering remain
authoritative. This does not keep VMs awake or stop stale workers. Durable workers,
transactional consumer receipts and external-effect idempotency remain necessary.
Ordinary success and non-opted-in subscriptions keep existing behavior. Upgrade
all schedulers before enabling policies and drain deferred work before rollback.
See [the protocol](../event-deferred-acknowledgments.md).

## Validation

Build changed packages and SDKs, validate OpenAPI, regenerate clients and check
SQLC parity. Local tests are neither added nor run under the user's standing
instruction. Database concurrency and end-to-end behavior remain unverified
locally; remote CI is required before release.

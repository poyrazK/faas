# ADR-1158: Retained lineage retry job discovery

Status: Accepted
Date: 2026-10-13
Related: ADR-1154, ADR-1155

## Decision

Expose GET on the existing subscription-scoped `execution-reconciliation/retry-jobs`
collection with read scope, MFA, no-store and the existing control timeout. Validate
account/app/subscription ownership before listing, using the existing subscription
or retained delivery-control identity. Return retained summaries only:
request UUID, subscription/app identity, state, selected and processed counts,
original creation/update timestamps and expiry. Case guards and recorded results
remain available through the existing individual GET. Processed counts evaluated
or skipped items, including changed/unavailable cases, and excludes cancellation.
Completion therefore does not imply every case was repaired.

Allow exact pending/completed/cancelled filtering, or all states when omitted.
Default page size is 50, maximum 200; zero selects the default. Query at most one
extra row to detect continuation. Exclude jobs whose original retention deadline
has passed, even before physical cleanup. Existing subscriptions with no jobs,
including those without execution coverage, return an empty array; unknown or
inaccessible subscription targets return 404.

Order newest first by immutable original created_at, then descending request UUID
for deterministic timestamp ties. The continuation cursor carries that timestamp
and UUID, not the mutable update timestamp. Parse a bounded RFC3339 timestamp and
nonzero UUID; timestamps must have PostgreSQL-compatible microsecond precision.
Use the tuple as an exclusive keyset bound without requiring the boundary row to
remain retained. Always apply tenant/app/subscription scope independently of the
cursor. The cursor is a pagination position, not an authorization credential or
reservation. Clients should pass it unchanged with the original filter.

Add an append-only migration for the subscription/creation/request-ID listing
index. No table or column changes, new persistent metadata, worker locks or
handler invocation are introduced. PostgreSQL projects counts from the existing
snapshot without returning its selections/results; the memory store returns
matching value summaries under its mutex. API, CLI and generated public SDKs
share the same discovery contract.

## Consequences

Operators can recover a lost request UUID and find work submitted by another
operator, then inspect its full details and original incarnation before cancelling.
Listings are live observations, not immutable pages or an audit archive. State
transitions, expiry and pruning may remove rows between pages. New jobs and jobs
entering a filtered status above the cursor require a fresh listing. A late-committed
creation can also appear above a passed boundary. No count of all historical jobs
or guarantee of snapshot completeness is provided. Existing job retention,
idempotency, cancellation, individual results, case repair and coverage semantics
remain unchanged.

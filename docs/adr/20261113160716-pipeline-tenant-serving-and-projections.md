# ADR-20261113160716: Tenant-scoped pipeline serving and column projection

- Status: implementation for local/staging qualification; production disabled
- Date: 2026-11-13
- Amends: ADR-1591

## Context

Trusted-reader allowlists expose every imported row. Customer portals need
owner-controlled tenant membership, and mirrors should copy only deliberately
selected columns. Neon onboarding also needs provider-specific guidance rather
than generic PostgreSQL settings instructions.

## Decision

Add an optional version-1 `serving` contract to creation and saved setup. Its
per-table column allowlists cover the complete selected table set, retain every
primary-key column, and include the tenant column when tenant access is enabled.
An omitted column map retains the existing all-column behavior. Explicit column
selection is immutable; changing it or adding another table to that contract
requires a newly reviewed pipeline. Existing scoped repair/removal/resync retains
the selected projection. Excluded columns are neither created on the mirror nor
included in native initial copy, CDC, schema evolution or verification queries.
Publication column lists minimize data copied by Gregale; PostgreSQL explicitly
does not treat them as an authorization boundary against a malicious subscriber.
Source credentials remain privileged replication credentials.

Tenant access specifies one text/varchar/UUID column shared by selected tables
and an account-owner-managed set of `(verified JWT subject, tenant value)` pairs.
No JWT tenant claim grants membership. New tenant pipelines derive their subject
envelope from these pairs and cannot also request whole-table reader grants.
RLS compares both values with the C collation. Null, unmatched or untrusted values
deny access. Membership changes use frozen preview/approval and the existing
transactional reader receipt, including source-independent revocation, pause,
disconnect, lost acknowledgements and later table gate reopening. In past-due
grace only strict membership reductions may be approved.

The catalog stores the serving contract separately from observed lifecycle state.
Lease-change triggers refuse claims of serving pipelines and saved setups unless
the claiming transaction advertises the new worker protocol. The new workers set
this marker locally in their claim transactions. N-1 workers can still reconcile
legacy pipelines, but cannot accidentally copy all columns or open all rows for a
contract they do not understand. Malformed contracts fail closed.

Neon onboarding provides reviewed direct-connection, project logical-replication,
role, IP-allowlist, continuous-compute billing, slot-expiry and restore guidance.
Provider identification changes diagnostics, never compatibility or capacity
admission. The existing positive <=1 GiB source WAL-retention requirement remains;
Neon's inactive-slot cleanup is not a byte-bounded retention qualification.
Live provider qualification requires explicitly configured staging identities,
disposable fixtures and reviewed fault controls. No credentials are configured in
the execution environment; local native acceptance cannot establish Neon support.

## Validation and rollout

Apply the expand migration before control-plane/worker binaries. Deploy capable
workers before enabling the new creation controls; an old worker cannot claim a
serving job and may defer its queue until a capable worker takes that job.
Native tests must
prove projected initial copy and CDC (including excluded-field updates), tenant
isolation and tenant-key moves, membership revocation and approval replay, repair,
verification and resync. Catalog tests exercise actual N-1 claim SQL against the
lease triggers, malformed contracts, and membership-only changes in grace.
Regenerate schema/sqlc, clone coverage, OpenAPI/SDKs, CLI reference and runtime
digest where affected. Keep account, provider, capacity and production gates.
Provider reports remain unqualified until the complete real-Neon drill passes.

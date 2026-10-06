# ADR-624 · Durable managed PostgreSQL idle policy changes

- **Status:** accepted for operator preview; live provider qualification required
- **Date:** 2026-10-06
- **Decision:** change scale-to-zero on an existing pinned database using the
  compute-change journal and reconciler introduced by ADR-623.

## Contract

`POST /v1/postgres/databases/{id}/compute-policy` requires a canonical nonzero
UUID `request_id` and an explicit boolean `scale_to_zero`. It reserves intent
before provider I/O and returns HTTP 202 with a progress URL.
`GET /v1/postgres/databases/{id}/compute-policy-changes/{request_id}` returns
current durable progress. Repeating the request returns its current receipt;
a different database, setting or operation kind conflicts. Class resize and
policy changes share a UUID namespace and one pending operation per database.
The existing resize API and its class-only contract remain compatible.

Policy changes preserve the service class, storage and restore limits, region,
major version, availability, exact dataset identity, bindings and credentials.
The database retains its confirmed specification while updating. A lease- and
generation-fenced transaction publishes the observed target specification and
completion together. The deferred database receipt verifies the full target
configuration, preventing older binaries from falsely completing new policy
intents. Rollback cannot discard policy history, including completed receipts.

A separate `ScaleToZeroUpdate` provider capability declares both directions.
It requires creation-time scale-to-zero support. API capability contract v3
reports that support separately from the plan's always-on entitlement. Turning
suspension off requires that entitlement. New requests require qualification,
plan and usage admission; accepted intents remain replayable and recoverable
when admission closes. The pinned backend, rather than the regional default,
decides whether an existing database supports changes.

Resize, deletion, unfinished credential changes/restores, cutover and clone
holds share the existing serialization boundaries. Clone-owned targets and
legacy databases without a data identity remain unsupported. There is no
customer cancellation or automatic rollback of an uncertain provider mutation.

## Adapter and qualification

Neon updates only `suspend_timeout_seconds` on the existing pinned primary:
300 seconds when enabled and -1 when disabled. Compute limits and project
defaults are not changed. Each attempt reads actual configuration first, adopts
an already applied target, waits for provider operations and rejects identity
or unrelated configuration drift. Mixed class and policy changes are rejected.
Existing connections may be interrupted; zero downtime is not promised.

Qualification v6 binds the new capability and requires a disposable policy
probe: observe always-on configuration and active compute, enable suspension,
observe actual suspend/wake, verify data and existing writer/reader credentials,
replay the target and restore the original policy. Missing proof, unsupported
probers, old approval versions and capability mismatch close admission.
The default qualification timeout is twenty minutes to accommodate both the
existing suspension probe and the new policy suspension probe.

## Validation

Memory and PostgreSQL tests cover explicit false, both directions, UUID/kind
conflicts, closed-admission recovery, atomic receipts and old-writer rejection.
Adapter tests cover exact PATCH fields, ambiguous response adoption, provider
work and drift. API, CLI and Go/Node/Python clients cover scoped access,
required booleans and stable request IDs. Live Neon qualification is a separate
operator rollout requirement; mocked API and local SQL evidence cannot replace it.

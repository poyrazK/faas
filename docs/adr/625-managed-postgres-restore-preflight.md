# ADR-625 · Provider-backed managed PostgreSQL restore preflight

- **Status:** accepted for operator preview; fresh live qualification required
- **Date:** 2026-10-06
- **Decision:** validate the exact pinned source and current provider recovery
  limits before reserving new PITR intent, and expose uncertainty explicitly.

## Problem

Catalog retention is configured intent. Provider retention can be shortened,
history can be absent and the provider dataset can drift independently. Checking
only the catalog accepts requests that cannot recover and leaves failed targets.
Conversely, checking today's history for an already accepted restore breaks
durable retries after the original point expires.

## Contract

`RestorePreflight` requires PITR support and the optional `RestoreSourceObserver`
interface. The observer reads exact lifecycle/data pins, current retention,
source readiness and timestamp lineage for restored sources. It supplies a
known necessary history floor, and may additionally supply authoritative retained
history bounds. Missing or contradictory evidence fails closed before reservation.
Legacy rows without an immutable data pin do not resolve mutable defaults.

New public restores and internal PITR clone reservations validate these limits
before their existing atomic source/target reservation. The store continues to
fence live source state and pins. Recovery of an existing matching receipt skips
new preflight and expiry checks. Restore's existing rollout admission still
applies; this ADR does not change closed-gate restore reconciliation.
Retained snapshot forks use their separate snapshot identity/proof contract.

`GET /v1/postgres/databases/{id}/recovery` requires account-scoped PostgreSQL read
access and interactive MFA, and returns `Cache-Control: no-store`. It uses the
database's pinned backend independently of the regional default or current
provisioning gate. It never reads credentials, connects to SQL, wakes compute
or mutates provider resources. Ordinary GET/list remain provider-I/O-free.

The API and `gregale postgres recovery DATABASE` distinguish:

- `limits_known`: necessary identity/retention/history limits validated, but
  recoverability of all timestamps remains unconfirmed.
- `available`: authoritative provider history bounds were also supplied.
- `unavailable`: a known condition prevents recovery.
- `unknown`: provider failure or missing/contradictory evidence.
- `unsupported`: no preflight capability, observer or immutable data pin.

Successful observations are fresh; errors never reuse old limits. Responses
contain only logical database identity, timestamps, effective retention, proof
flags and stable error codes. No private IDs, endpoints or error text escape.
Capability discovery contract v4 reports `restore_preflight`; new PITR support
requires it. The Go, Node and Python SDKs share the OpenAPI schema.

## Neon evidence and limits

Neon reads the owning project and exact branch with two GETs. It checks project,
organization, physical/logical region, PostgreSQL major, branch identity,
readiness, creation metadata and any recorded timestamp lineage. Missing/null
retention differs from an explicitly disabled zero. Effective retention is the
smaller provider/catalog limit. Project creation is a necessary lower floor.

The [Neon API specification](https://neon.com/api_spec/release/v2.json) and
[branch details endpoint](https://api-docs.neon.tech/reference/getprojectbranch)
do not document authoritative earliest retained WAL bounds. Branch creation or
reset times cannot establish inherited history. The adapter therefore reports
`limits_known` and leaves `history_bounds_known` false. An in-range timestamp
can still fail at restore time. Neither this metadata read nor authoritative
bounds eliminate retention changes between preflight and provider mutation.

## Qualification and rollout

Qualification v7 binds the capability declaration and requires a metadata probe
for the exact inspected data pin, acceptance of the actual SQL restore probe
point, and rejection of a point below the known lower limit. The mutation uses
that pin, and the existing restore probe must still verify earlier committed
data, target credential isolation and cleanup. A successful point probe does
not certify a complete history interval. Disabled-retention qualification specs,
missing observers/pins/proof, capability mismatches and prior approval versions
cannot authorize this capability.

No schema migration or persisted recovery cache is added. Providers can replace
Neon by implementing and qualifying the same evidence contract; retention math
alone cannot qualify an adapter. Fresh live Neon v7 qualification and lifecycle
smoke remain required before reopening admission. Local SQL and mocked provider
tests do not substitute for that run.

## Validation

Tests cover shorter/disabled retention, source and backend pinning, missing or
invalid metadata, timestamp boundaries, optional authoritative bounds, source
lineage drift, cancellation/timeouts, no reservation after rejected requests,
and replay after expiry/provider outage. Adapter tests permit only exact GETs.
API/CLI/SDK tests preserve uncertainty, scoped access and safe serialization.

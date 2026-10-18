# ADR-1660: Data API connection-capacity observations

Status: Accepted

## Context

Data API connection-budget reports aggregate potential consumers but previously
had no source of verified capacity. PostgreSQL `max_connections`, current idle
slots and a provider compute class do not establish a tenant's usable allocation.
The existing provider-health observer deliberately reads metadata without SQL.

## Decision

Add a separate operator command, `managed-postgres-capacity`, that combines a
trusted qualified allocation record with a bounded read-only SQL observation.
Run it once or with `--watch`; each iteration rereads the allocation file. The
command is opt-in and is not started by apid or customer traffic. It requires
the existing managed PostgreSQL configuration and control-plane `DATABASE_URL`.
Apply the normal migrations before starting it; it never migrates on its own.

The allocation record binds account, database, immutable backend fingerprint,
provider lifecycle identity, frozen dataset and desired generation. It records
an explicit `provider_api` or `native_qualification` source, the SHA-256 digest of
its independently retained source report, an observation time and an expiry at
most five minutes later. The file is a trusted operator attestation. The worker
does not verify a provider signature, discover an allocation automatically or
turn a caller-supplied number into independent qualification. A qualified source
must cover the tenant's actual connection path and role restrictions. Operators
must refresh this input from that source, rather than extend an old receipt.

The Neon adapter reuses direct maintenance credential validation for the exact
frozen branch. It checks project, organization, region, major version, endpoint
and direct host before SQL, then checks the endpoint again after SQL. It refuses
idle/moving compute instead of deliberately waking it for observation. An
endpoint can still suspend between metadata and connection establishment.
Connection options are rebuilt with certificate verification; provider-supplied
startup SQL is discarded. The worker opens one short-lived SQL connection using
the existing owner credential and does not issue roles, change grants, install
objects or create a persistent tenant pool. The metadata-only health contract
is unchanged.

A read-only SQL transaction verifies database, session role and PostgreSQL major
and reads server reservations plus database and observer-role connection limits.
The resulting ceiling is the minimum of the qualified allocation,
`max_connections - superuser_reserved_connections - reserved_connections`, and
each finite database/observer-role limit. Zero is a verified ceiling. This is
neither a count of free slots nor proof that every application role has sufficient
capacity; consumer configuration and external usage remain separately qualified.

The control plane stores observations under a short database lease. Claiming
clears previous evidence immediately. A result can finish only under its live
lease and unchanged account/backend/provider/dataset/generation. Reads require
the same identity and a fully observed desired generation. Cache validity is at
most 30 seconds from the start of observation, clipped to allocation expiry.
An invalid file, permission failure, SQL failure, stale receipt or replacement
therefore cannot reuse an earlier success.

Existing budget endpoints and `gregale data-api health` consume only this cache.
Capacity adds optional source/check/expiry fields; private provider identities,
SQL settings, source digests and credentials stay out of customer responses.
Older clients retain the existing fields. A verified capacity does not make the
overall budget known while runtime or external-consumer coverage is incomplete.
`enforced` remains false.

## Consequences and validation

Operators can supply real capacity evidence without adding privileged SQL work
to an API read. Freshness and identity checks provide conservative failure
behavior across worker restarts, replacement and overlapping observers.

Tests cover allocation identity/provenance/expiry, SQL ceilings including zero,
native SQL session checks, concurrent and late workers, durable restart reads,
generation/dataset replacement, failed observation invalidation and redaction.
The adapter is tested against real local PostgreSQL with a simulated provider
API. Live provider qualification and automated allocation exporters remain
separate operational work. Aggregate budget enforcement remains a later change.

See the [operator procedure](../runbooks/DataAPIConnectionCapacity.md) and
[Data API architecture](650-schema-generated-data-api.md).

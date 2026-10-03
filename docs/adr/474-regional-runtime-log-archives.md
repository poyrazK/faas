# ADR-474: Regional runtime log archives and durable ownership

Status: Accepted; internal rollout pending native acceptance
Date: 2026-10-02
Extends the daily archive lifecycle in issue #562 and regional artifact routing in ADR-470.

## Context

Runtime log archives used one global S3 bucket independently of app placement.
The archive endpoint authenticated the app but accepted an arbitrary caller supplied
instance ID. Instance garbage collection also removed the ownership evidence needed
to authorize historical reads. A local cumulative daily gzip alone cannot preserve
history when a migrated instance changes workers or loses its local spool.

## Decision

Vmmd captures a binding from scheduler-owned Instance → Deployment → App → Account
rows before cold boot or snapshot restore. Capture also verifies the physical
compute-node UUID and the wake envelope's owner fields. Startup captures retained
instances on that node. Ring eviction accepts only an in-memory captured identity;
no database or filesystem operation runs under the ring mutex. Capture failure is
observational: it disables archive acceptance for that instance and logs the reason
without failing an otherwise valid wake. Jobs and builders without an app-instance
row do not acquire an app archive binding.

The binding persists independently of instance and deployment rows. A daily catalog
reserves `regions/<compute-region>/faas-logs/<instance>/<YYYY>/<MM>/<YYYY-MM-DD>.jsonl.gz`
before PUT and records compressed bytes, SHA-256 and publication time after success.
Empty placement keeps the established unprefixed S3 key and bucket. PostgreSQL
triggers enforce immutable binding identity and canonical daily keys. The catalog
and immutable accepted-fragment receipts are additive and survive worker restarts;
app/account deletion cascades them only after storage cleanup. Migration replay
preserves data, and downgrade refuses while retained bindings exist.

Publication and app/account hard deletion share an app-scoped advisory lock. App
and account status fence captures, reservations and publication. Accepted fragment
receipts commit atomically with the daily descriptor. A retry consumes a previously
accepted sealed fragment before merging newer partials, including when another
worker has published in the meantime. A fresh or stale spool reconstructs earlier
history from the remote object only after verifying its cataloged size and digest.
An unavailable or corrupt prior object prevents replacement and retains local
pending lines for retry.

Regional archives use the existing explicit artifact routes and their route guard.
Daily objects are mutable: archive reads and writes bypass local artifact caches
while preserving every selected route and its validation. A cached older daily
object never substitutes for the current catalog descriptor.
There is no fallback to the global archive bucket or another region. Regional-only
operation does not require a global S3 credential envelope. OCI stores use a fixed
`faas-logs` repository with reversible instance/day tags. Native UUID instance IDs
fit the OCI tag bound; an unsupported oversized legacy opaque ID fails publication.

Gatewayd-internal resolves the catalog under the authenticated app/account and
checks the immutable app region and exact canonical key before opening storage.
It streams gzip and verifies cataloged compressed size and SHA-256 before emitting
`archive_complete`. Corruption emits `archive_degraded`; already streamed owned
lines may precede that terminal frame. A missing or foreign descriptor emits
`archive_missing`. Historical uncataloged legacy objects are readable only while a
retained instance proves ownership of an empty-placement app. The SSE response and
SDK envelope stay compatible. The request deadline also bounds a blocked fetch.

Grace cleanup deletes all reserved keys, including uncommitted publications, under
the same lock before hard deletion. Account cleanup includes app tombstones omitted
by the ordinary app inventory. Missing credentials, a missing route, or a storage
failure retains ownership metadata for retry. Other regions' objects are untouched.

## Rollout and qualification

Drain regional wakes and archive publishers, install the migration, then upgrade
vmmd, gatewayd-internal and apid together. Keep independent vmmd/apid spools.
Configure `FAAS_STORAGE_REGIONS_FILE` on each publisher, reader and cleanup worker;
API and account cleanup workers need every participating route. Legacy bucket
configuration continues to use `FAAS_LOG_ARCHIVE_*` and its existing credentials.

Drain legacy pending spools before upgrading. A file without a durable instance
binding cannot be attributed from a worker's region, guessed app, or its filename.
Existing global archives of regional apps are not automatically relocated or
adopted; backfill verified ownership and move bytes in a separate operator migration.
Configure remote retention for the archive prefix/repository in every regional
store. Local retention still does not discard unshipped outage backlog.

Portable tests cover routing, retained ownership, direct SQL guards, failed metadata
publication, interleaved writers, corrupt reads and deletion/publication ordering.
Native cold boot, restore, migration, recovery and leakcheck require the dedicated
Linux x86_64/KVM fleet. Portable tests and cross-compilation do not qualify those
paths. Regional admission remains internal and default-off pending those gates.

## Remaining work

ADR-461 supplies catalog-aware remote retention and failed-PUT orphan collection.
Cleanup of permanently expired or unattributed local spools, ownership backfill for
uncataloged old remote objects, job archive history, persistent services, regional
ingress and independent control planes remain open. PostgreSQL metadata remains shared. Artifact and archive
routing alone does not promise end-to-end data residency.

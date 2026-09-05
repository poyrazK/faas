# ADR-151 · Provider-neutral customer object storage

- **Status:** accepted for an opt-in preview; public launch pending provider qualification and billing integration.
- **Date:** 2026-09-05
- **Decision:** add customer-owned logical buckets backed by interchangeable S3 services, without adding persistent VM disks or operating storage nodes.

## Context

Gregale rents compute capacity and will launch in its own `us-east-1` region.
The first three months should use managed object storage. Afterwards, renting
storage nodes and running an S3 service such as Ceph RGW must not require a
customer API or dashboard rewrite. This extends the bring-your-own-object-store
part of ADR-046 / `docs/storage.md`; the stateless compute contract and existing
financial-model rates are unchanged.

## Decision

`apid` owns the logical bucket catalog and validates account/app ownership.
Buckets carry an environment scope for organization; scope is not an additional
security boundary within an app. The provider package owns upstream S3 calls.
SQLC-backed PostgreSQL rows persist backend ID, placement fingerprint and an
opaque physical bucket name. No upstream secret is stored in customer records.

Backend factories implement `objectstorage.Provider`. One generic S3 driver
covers create/delete bucket, CORS, paginated object listing, object deletion and
signed GET/PUT requests. Configuration separates Gregale region from the
upstream signing region and endpoint. Credential references resolve at startup.
Provider-specific IAM and billing APIs are deliberately outside this driver.

The default mapping selects placement only when reserving a new bucket. An old
bucket always resolves its stored backend ID and fingerprint. Removing or
repurposing that backend fails closed; it never falls back to another provider.
Credential rotation does not change placement. API replicas must share config.

Creation/deletion use durable two-minute leases and bounded upstream calls.
Failed setup is retried with the same app/scope/name and physical bucket.
Apid also reconciles pending intents automatically using durable retry metadata,
bounded backoff, and lease-token fencing. Configuration failures use a slower
probe cadence. Recovery never deletes unidentified upstream buckets.
Nonempty deletion fails and restores access; successful deletion leaves a
tombstone. App deletion is rejected while active buckets exist. There is no
recursive deletion, automatic migration, or automatic orphan cleanup.

Objects travel directly between client and S3. Signed URLs expire in five
minutes by default (15-minute ceiling), are reusable bearer capabilities, and
are never cached by the API. Uploads bind the declared size through signed
Content-Length; zero-byte uploads bind the empty Content-MD5 digest. Exact CORS
origins permit browser PUTs. Downloads are attachments. No customer gets the
provider-wide key. This is a Gregale bucket-management API, not a Gregale-hosted
S3 protocol endpoint or a native S3 access-key service.

## Financial and launch boundaries

The global, hot-applied `s3_enabled` runtime-config flag defaults off, independently
of the bootstrap provider registry. Off blocks provisioning and new signed URLs,
but preserves metadata, authorized cleanup, and recovery of deletion intents.
Already-issued URLs expire normally; in-flight operations may finish.
Enabling it is an operator opt-in to an unmetered
preview, not an entitlement included in existing plans. Existing runtime
storage rollups are not object-storage usage. Per-app bucket and per-upload
limits are configurable safety controls, not a total byte quota, request budget
or spend cap. Direct traffic cannot be billed from signed-URL counts.

Before public launch: approve reseller/subprocessor terms and regional promises;
qualify a backend against the runbook; integrate inventory/usage and request/
egress costs, pricing, abuse controls, and account-deletion cleanup. Native
customer S3 keys require a separate tenant-scoped IAM adapter (or a deliberate
S3 gateway design), not exposing the operator key.

## Validation

Provider tests verify signatures, zero-byte integrity, S3 protocol requests,
error sanitization and placement fencing. API tests exercise ownership, scopes,
retries, deletion and default switching. MemStore/PostgreSQL tests cover quota
races, leases, tombstones and app-deletion guards. Browser upload tests verify
that Gregale credentials never accompany direct provider requests. Live backend
qualification remains a prerequisite; mocked protocol tests are not that proof.

## Consequences

The dashboard and platform API remain unchanged when adding an S3 backend.
Moving existing data still requires copying and verifying objects, stopping
writes and waiting for old signed URLs to expire before an explicit cutover.
Already-issued URLs continue to point at the old endpoint. No DNS trick can
make an upstream signature portable. Retain the old backend until migration
and recovery windows have completed. See [operator runbook](../object-storage.md).

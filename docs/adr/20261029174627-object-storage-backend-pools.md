# ADR: A region places new buckets across a pool of storage backends

Date: 2026-10-29
Status: Accepted
Related: [ADR-156](156-object-storage-accounting.md), [ADR 20261023223000](20261023223000-object-storage-bucket-scoped-availability.md), [ADR 20261025111015](20261025111015-object-storage-managed-gateway-and-restore-reconcile.md)

## Context

Gregale is moving managed object storage to OVH's S3 service. Every customer
bucket is its own provider bucket: apid reserves a catalog row with a
`backend_id`, `backend_fingerprint` and `physical_name`, then creates that
bucket upstream. The backend never changes for the bucket's lifetime, and the
gateway, inventory, deletion and recovery all resolve the backend from the
row.

New buckets in a region all go to one backend, `defaults[region]` in the
operator registry. An OVH Public Cloud project allows 100 buckets by default
and at most 1,000 after a support request. With up to 10 buckets per app, one
region can therefore hold at most 1,000 customer buckets. After that, every
`CreateBucket` fails upstream and the bucket stays stuck in provisioning.

The provider fact behind this is a per-project quota, and S3 credentials are
per project too. Raising the quota is not enough; Gregale has to be able to
use more than one project per region.

Copying between buckets is already refused when the two buckets are on
different backends (`s3_copy_sources.go`): a server-side copy needs one
provider account.

## Decision

1. **A region's new buckets go to a pool of backends.** The registry gains
   `pools`, an ordered list of backend IDs per region. A region in `pools`
   may omit `defaults`; if it has both, the default must be the pool's first
   backend. A region without a pool keeps today's single default. Backends
   outside every pool and default keep serving their existing buckets, as
   today.
2. **A backend can declare a bucket ceiling.** `max_buckets` (1 to
   `api.MaxObjectStorageBackendBuckets`) caps the live buckets placed on it;
   0 means no ceiling. Every member of a pool with more than one backend must
   declare one, set below the provider quota so the provider's own limit is
   never what refuses a bucket (for OVH, 950 of 1,000).
3. **A pool's backends offer the same features.** A bucket's features must
   not depend on which backend it landed on, so the members of a
   multi-backend pool share their driver, Object Lock settings, encryption
   algorithms and allowed origins, and declare no customer encryption keys
   (a key lives in one provider project).
4. **An account's buckets stay together.** A new bucket goes to the pool
   backend that holds most of the account's live buckets, if it has room.
   Otherwise, and for an account without buckets, it goes to the first
   backend in pool order with room. Cross-bucket copies, which need one
   backend, keep working until an account's backend fills.
5. **Placement is atomic.** The choice runs inside the existing bucket
   reservation transaction, after its account and app locks, under one
   advisory lock per region (`object-bucket-placement:<region>`). Concurrent
   creations can never pass a ceiling. A deleted bucket stops counting once
   its row is `deleted`. An isolated environment clone creates its bucket on
   its source's backend, outside placement, so a clone whose source backend
   has a ceiling is refused at planning (`isolated object-storage environment
   cloning is not supported`). Production's gateway-safety accounting refuses
   these clones already.
6. **A full region refuses before touching the provider.** When no pool
   backend has room, apid creates no row and answers `503
   object_storage_region_full`. Existing buckets are unaffected.
7. **Operators see capacity before it runs out.** Every minute, apid's
   accounting loop exports `faas_object_storage_backend_buckets{region,backend}`
   for every configured backend, `faas_object_storage_backend_bucket_limit`
   for each pool backend with a ceiling, and
   `faas_object_storage_region_full{region}`, 1 while no pool backend has
   room. `FaasObjectStorageRegionNearlyFull` warns when a region's ceilings
   are 80% used for 30 minutes; `FaasObjectStorageRegionFull` warns while
   creation is refused. The runbook adds a project: create it and its S3
   user, ask OVH for the bucket quota, add the backend with `max_buckets` to
   the end of the pool, restart `s3-gatewayd` and `gatewayd-public`, then
   apid.

## Consequences

- One region can hold `Σ max_buckets` buckets: ten OVH projects at 950 each
  hold 9,500. Capacity grows by configuration, without code changes.
- Each project is a separate backend: its own S3 user and keys, its own
  fingerprint, and its own line in `gregalectl object-storage reconcile`.
- The gateways must know a backend before apid places buckets on it, so a
  new backend is rolled out to `s3-gatewayd` and `gatewayd-public` first.
- An account whose backend fills spreads to the next one, and copies between
  its old and new buckets are refused as they are today across backends.
- Bucket creation in one region is serialized by the placement lock. Bucket
  creation is rare and the lock is held for the reservation only, never
  across the provider call.
- A release older than this change refuses a registry with `pools` or
  `max_buckets`, because unknown fields fail closed. Roll the configuration
  out only after every daemon that loads it (apid, `s3-gatewayd`,
  `gatewayd-public`, `gregalectl`) runs a release that reads it.
- Taking a backend out of its pool stops new placements on it; its existing
  buckets keep working.

## Alternatives considered

- **Many customer buckets as prefixes in a few shared provider buckets.**
  Removes the quota entirely, but versioning, Object Lock, default
  encryption and the empty-bucket proof before deletion are per provider
  bucket. Customers sharing one could not choose them independently. A
  redesign, not a placement change.
- **Creating the provider bucket on first upload.** Saves empty buckets but
  hits the same quota.
- **Fewer buckets per app.** Only delays the limit.
- **Least-full placement.** Spreads every account across projects, so
  cross-bucket copies would fail far more often. Filling in order also keeps
  a newly added project empty until it is needed.

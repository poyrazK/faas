# ADR-20261119065000: Customers choose Object Lock when they create a bucket

Date: 2026-11-19
Status: Accepted
Amends: [ADR-564](564-durable-bucket-object-lock.md)
Related: [ADR-563](563-native-object-lock-protocol.md), [ADR-20261029174627](20261029174627-object-storage-backend-pools.md)

## Context

ADR-564 enables Object Lock on an existing bucket. Gregale creates every
provider bucket without Object Lock, and `BucketObjectLockService` later sends
`PutObjectLockConfiguration{ObjectLockEnabled: Enabled}` to switch it on.

OVHcloud Object Storage, which replaces GCS for customer buckets
(`docs/runbooks/GcpToOvhMigration.md`), allows Object Lock only when the
`CreateBucket` request asks for it (`x-amz-bucket-object-lock-enabled: true`,
`ObjectLockEnabledForBucket`). It can never be added to an existing bucket.
AWS behaved the same way before 2023. On an OVH backend with
`object_lock.enabled: true`, the ADR-564 enablement step therefore fails.
Versioning can still be enabled on an existing OVH bucket, so ADR-545 and
`BucketVersioningService` are unchanged.

The owner considered making every bucket Object Lock enabled. That makes every
bucket permanently versioned, so every overwrite and delete keeps billable
history. The owner chose instead: the customer asks for Object Lock when
creating the bucket, and only those buckets get the creation flag.

## Decision

1. **Backend capability.** `ObjectLockConfig` gains `creation_only`. It
   declares that the backend accepts Object Lock only at bucket creation.
   It requires `object_lock.enabled`, and the backend's provider must create
   Object Lock buckets (`ObjectLockBucketCreator`). OVH backends set it; AWS
   and S3-compatible backends that allow later enablement leave it off.

2. **Creation intent.** A create request carries `object_lock: true`:
   - apid REST `POST /v1/apps/{slug}/buckets` (and the org route) field
     `object_lock`;
   - the CLI flag `gregale add bucket --object-lock`;
   - the dashboard's create form (a checkbox posting `object_lock`), which
     forwards to the same handler;
   - the S3 gateway: Gregale's gateway does not create customer buckets
     today (bucket-level `PUT` without a subresource answers `NotImplemented`).
     When it does, `x-amz-bucket-object-lock-enabled: true` maps to the same
     intent. No other header turns it on.

   apid refuses the request before reserving anything when the region's
   placement backend does not offer bucket Object Lock configuration
   (`501 object_storage_unsupported`). The owner's plan is checked inside the
   reservation transaction with the same rule as an ADR-564 enablement: a plan
   without Object Lock retention gets `403 object_lock_plan_limit` and no
   bucket.

3. **Intent is journaled with the reservation.** The reservation transaction
   inserts the bucket's ADR-564 Object Lock journal row with the accepted
   request `{Enabled: true}` (revision 1, `waiting`, `enabled_required`). The
   journal guard accepts exactly that row shape on a `provisioning` bucket and
   accepts updates to it that stay `waiting` and undispatched, so the recovery
   worker can defer it. Everything else still requires a `ready` bucket.
   Provisioning reads the journal and, when `enabled_required` is set, creates
   the provider bucket with `ObjectLockEnabledForBucket: true`. A retry after
   a lost response or an apid restart reads the same journal, so recovery
   always sends the flag. On every backend, not only creation-only ones,
   creation intent uses the creation flag.

   Once the bucket is ready, ADR-564 reconciliation runs unchanged: it
   observes native `Enabled`, requests Enabled versioning, waits for the
   versioning cutover and all-version inventory, and finishes `ready` without
   a native PUT because the desired configuration already matches.
   Repeating a create with `object_lock: true` for an existing bucket whose
   journal does not require Object Lock is a `409 object_storage_conflict`.

4. **Lock-enabled buckets are permanently versioned.** The provider enables
   versioning with Object Lock, and ADR-564's journal latch requires Enabled
   versioning for good. Suspension stays refused. Customer docs say this at
   the flag.

5. **Later enablement on creation-only backends.** A request to enable Object
   Lock on a bucket whose journal and native state show it is not already
   enabled is refused before any intent is stored:
   - control API: `409` with stable code `object_lock_requires_new_bucket`;
   - S3 `PutObjectLockConfiguration`: `409 InvalidBucketState`, AWS's code
     for the same case.

   Changing the default retention of a bucket that already has Object Lock is
   still accepted. Reconciliation on a creation-only backend never sends a
   native PUT to a bucket that is not natively enabled; such a row retries
   with `provider_unsupported`.

6. **Backend pools.** ADR-20261029174627 already requires pool members to
   share the whole `object_lock` configuration. That now includes
   `creation_only`, so a bucket created with Object Lock gets the same
   behaviour on whichever pool member it lands on.

7. **Release default.** `object_lock.enabled` stays off by default, as
   ADR-564 requires for the pre-1.0 release. With it off, `object_lock: true`
   at creation is refused as unsupported.

## Consequences

- Customers must decide at creation. Moving data into a new lock-enabled
  bucket is the only way to protect an existing bucket on OVH.
- The journal row now appears for a bucket from its reservation. The recovery
  worker defers a provisioning bucket's row without calling the provider.
- Not yet qualified against OVH itself. Before `creation_only` is turned on in
  production, an operator must create a lock-enabled bucket on the real OVH
  backend, read back `GetObjectLockConfiguration`, and confirm that a later
  `PutObjectLockConfiguration` on a plain bucket returns the error mapped
  here.
- Follow-ups: dashboard checkbox, regenerated Node/Python SDK bodies, and
  customer docs for the flag.

## References

- [AWS: S3 Object Lock configuration](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lock-configure.html)
- [OVHcloud: Object Lock (WORM)](https://help.ovhcloud.com/csm/en-public-cloud-storage-s3-object-lock)

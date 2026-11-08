# ADR-20261103160000: Full environment clones, slice 3: buckets at the data point

Date: 2026-11-03
Status: Accepted
Amends: ADR-590 (complete project-environment clones), ADR-20261024224230 (slice 2)

## Context

Slice 2 copies managed databases at one data point, with no writer barrier.
A source that binds a bucket was still refused at admission
(`object_buckets: data_checkpoint_unavailable`). Capture refused it too
(`errCloneCheckpointUnavailable`), because ADR-590 waited for a coordinated
checkpoint that would line a bucket manifest up with the database point.

Most of the copy machinery for buckets already exists:

- **Version manifests:** `objectstorage.CaptureObjectManifest` picks, for each
  key, the newest version written at or before a point. A key whose newest
  such version is a delete marker is dropped.
- **Verified copies:** `CopyAndVerifyObjectVersion` copies one pinned version
  and checks the copy against it.
- **The rest of the pipeline:** operation-owned target buckets, credential
  preparation and publication.

Four gaps stopped any of it from running:

- **No admission path.** A source with buckets was refused outright.
- **Unusable retention rule.** The capture and copy helpers required every
  pinned version to carry S3 Object Lock in COMPLIANCE mode. Customer buckets
  never have that.
- **Unhandled bucket tables.** Bucket settings tables and gateway bookkeeping
  tables had no clone strategy, so any real bucket blocked admission.
- **No compensation.** Direct compensation could not remove target buckets.

## Decision

1. **Buckets are copied as of the slice-2 data point.** There is still one
   point per clone: the databases are restored at it, and every bucket's
   manifest is captured as of it. As in slice 2, no writer barrier is taken.
   - Capture records the point and the planned bucket resources, and the
     clone stays `capturing`.
   - Once the point is `cloneDataPointSettle` (30 s) old, the coordinator lists
     each bucket's versions as of the point. It reserves and provisions the
     operation-owned target, and commits the manifest.
   - Only then does the clone move to `copying`. The wait covers clock
     differences between the control plane and the provider.

2. **No provider retention is required; a lost version fails the clone.**
   Clone workers drop the Object Lock COMPLIANCE requirement. Each copy is
   verified against its pinned version.
   - **During the copy:** if a pinned version is permanently deleted, or no
     longer matches, the clone fails with `source_changed_during_capture` and
     is compensated. The customer retries.
   - **Between the point and the listing:** a version permanently deleted in
     that window is not seen, so the manifest holds what the history still
     holds. Ordinary deletes only add delete markers and are read correctly.

3. **Admission qualifies S3-compatible backends with versioning.**
   `object_buckets` no longer blocks. Each source bucket is checked in the
   capture transaction and refused by name (`resource_coverage.object_buckets.<name>`):
   - `object_versioning_required` when versioning is not enabled and
     propagated. Admission does not turn versioning on for the customer.
   - `object_storage_provider_unqualified` when the bucket's backend is not an
     S3-compatible provider. OVH Object Storage is the expected production
     backend. GCS and others stay refused.

   Versioning state is part of the frozen bucket definition, so turning it off
   during a clone fails the clone as a source change.

4. **Bucket settings still block; gateway bookkeeping is derived.**
   - **Settings that block:** CORS, lifecycle, notifications, encryption,
     Object Lock, upload routes and per-version protection. A source holding
     any of them is refused with `object_bucket_setting_unavailable`, naming
     the table. Copying these settings is a later slice.
   - **Bookkeeping the target derives:** `object_bucket_versioning`,
     `object_storage_key_grants` and `object_version_references`. The target
     gets its own rows: versioning is turned on for it, the inventory sweep
     measures its contents, and version references are recorded as the
     gateway serves it.

5. **The target bucket is versioned.** Before copying into a target, the clone
   requests versioning on it and waits until it is on. The stage then keeps
   its source's history semantics and can itself be cloned.

6. **Copied data counts against the account's storage limits.**
   - **At admission:** a clone is refused when the source buckets'
     conservative capacity would take the account past `account_bytes` or
     `account_keys` under the effective policy. The problem carries the
     limit, the observed value and the limits docs URL.
   - **After the manifests are pinned:** the exact totals are checked again.
     A clone past the limits fails with `object_storage_capacity_exceeded`
     and is compensated.
   - **Usage of the target:** measured by the regular inventory sweep. A new
     bucket has no usage row, so it is due first. The target serves no
     traffic until the clone publishes.

7. **Direct compensation deletes target buckets.** Clones with
   `object_storage` resources are directly compensable. Compensation revokes
   each target's credentials and claims the bucket for deletion. Owned-bucket
   cleanup empties every version before deleting the provider bucket, and the
   bucket worker finishes what one batch leaves. The clone ends compensated
   only once every target bucket is deleted (`clone_target_buckets_deleting`
   until then).
   - **Deadline:** a clone with buckets gets the slice-2 database deadline
     (2 h) instead of the 30 min data-less one.

## Consequences

- **What clones now:** sources with managed databases and plain versioned
  buckets on an S3-compatible backend, behind the existing dark switches
  (`full_environment_clones_enabled`, `full_environment_clone_admission_enabled`).
- **Consistency:** the copy is point-in-time across databases and buckets,
  with the same no-barrier semantics as slice 2. Writes that straddle the
  point may appear in one store and not another.
- **Not covered:**
  - Copying bucket settings.
  - Buckets on GCS or other backends.
  - Protecting pinned versions from permanent deletion while a clone runs.
    The decided lean is to fail and retry, not to hold.
- **Unused interface:** `ObjectVersionRetentionObserver` stays in
  `pkg/objectstorage`, and clone workers no longer call it.

## Amendment (2026-11-05): Snapshot listing requires completion evidence

S3 snapshot capture uses the same validated version-list adapter as public
version history. A page must explicitly report whether it is truncated, with
consistent continuation markers and complete version identities, sizes and
timestamps. Missing or contradictory pagination evidence fails capture rather
than publishing an empty or partial manifest. Continuation cursors retain their
existing wire shape and are validated before any native request.

## Amendment (2026-11-05): Recover copies before repeating them

The S3 snapshot copier stamps a reserved receipt bound to the source bucket,
target bucket and exact manifest item. It uses one native `CopyObject` attempt
with destination `If-None-Match: *`. Matching target proof is adopted after a
lost native acknowledgment, verification failure or lost `CopiedAt` checkpoint;
an unrelated existing target is refused. Verification hashes both the pinned
source version and the proven destination version. A late request or overlapping
worker cannot create another retained destination version on a backend honoring
the native condition. Backend qualification must cover conditional copy.

This slice copies selected objects up to the native single-copy ceiling,
`api.MaxObjectSinglePutBytes` (5 GiB), independently of a deployment's upload
profile. Capture refuses a larger selected object before committing its copy
plan. Pending objects in old manifests are requalified before any new copy;
previously verified copies remain resumable.
The permanent `object_storage_copy_size_unsupported` failure compensates the
operation-owned target instead of retrying until the deadline. Native multipart
snapshot copying requires its own recovery qualification and remains outside
this slice. Normal managed multipart uploads retain their total object limit.

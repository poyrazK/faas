# ADR-20261107214517: GCS buckets start in all-version accounting

## Status
Accepted. Amends [ADR-20261023223000](20261023223000-object-storage-bucket-scoped-availability.md)
decision 4; consistent with [ADR-628](628-gcs-tracked-writes-and-native-generations.md).

## Context

GCS reports a never-versioned bucket as `Suspended` and gives every object a
generation, so Gregale cannot tell a new bucket from one that keeps old
versions. ADR-628 and ADR-20261023223000 therefore account every GCS bucket
in all-version mode. The conversion happened lazily: the first object's
generation and the first delete's `Suspended` observation each started a
version inventory, and the bucket fenced writes and deletes until it
finished.

Measured on production GCS on 2026-11-07 with a fresh bucket: after the first
object, writes were refused `409 OperationAborted` ("pending work") for
5 m 13 s; the first delete was then refused for 42 s. Afterwards gateway
writes, signed-URL writes and deletes all worked. The customer journey
`TestObjectStorage` failed on exactly this.

## Decision

- A provider declares `ReportsNeverVersionedAsSuspended()` when it has no
  never-enabled versioning status. GCS does; S3-compatible providers do not.
- When apid creates such a bucket, after the provider bucket exists and before
  the bucket becomes `ready`, one transaction records the state the lazy
  conversion would reach: a completed, verified all-version inventory of the
  empty bucket; a `ready` versioning row with `Suspended` desired and observed,
  `versions_required`, `propagation_until` at the transaction time and that
  inventory as its capacity job; and bucket usage in `all_versions` scope with
  a fresh empty observation.
- The bucket is provably empty: Gregale created it moments earlier, nothing
  else holds its credentials, and no customer request reaches a bucket that is
  still `provisioning`. The transaction runs only while the bucket is
  provisioning and has no usage, versioning, inventory, grant or write row, so
  a retried or recovered creation never overwrites real accounting.
- A failure is logged and never fails the creation; the bucket then converts
  lazily as before.
- Existing buckets are unchanged. Those already converted keep working; a
  GCS bucket created before this change converts on first use as before.

## Consequences

New GCS buckets accept their second write and first delete at once. The
existing database triggers accept the recorded state unchanged (the versioning
cutover requires a completed, verified all-version inventory created no
earlier than `propagation_until`). Periodic all-version inventories still run
for GCS buckets as for every all-version bucket.

# ADR-540: Native S3 version capacity inventory

## Status

Accepted — 2026-10-02. Extends ADRs 533 and 539.

## Context

Historical receipts can now confirm retained S3 writes. ADR-539 conservatively
blocks capacity reclamation once it detects native versions because current
object listing omits their storage. A maximum grant per key also cannot admit
successive writes safely when each creates another retained object.

S3's [version listing](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectVersions.html)
returns data versions and delete markers with paired key/version continuation
markers. [Delete markers](https://docs.aws.amazon.com/AmazonS3/latest/userguide/DeleteMarker.html)
consume storage equal to their key's UTF-8 byte length. A simple DELETE can
create another marker, including in a suspended bucket.

## Decision

Add an optional native inventory adapter. S3 lists the whole bucket without
prefix/delimiter aggregation, at most 1,000 entries per request, with URL key
encoding and SDK retries disabled. Count every returned data version, including
null versions, at its actual size; count each marker at the key's UTF-8 byte
length. Reject malformed/truncated pages, duplicate identities and invalid
continuations. Native IDs and paired cursors remain private.

Use the existing capacity job and write fence. Native observations or an
existing all-version baseline select `inventory_scope=all_versions`. Pending
writes, unsafe grants and live/incompletely reclaimed multipart sessions retain
their existing conservative readiness rules. Every native page commits its
identity hashes, counters and bounded cursor in an account-locked, lease-checked
transaction. PostgreSQL staging rows enforce unique SHA-256(key + NUL + native
version ID) identities across pages and unique continuation hashes. The worker
holds only one page, not the complete identity set. Memory stores implement the
same contract, keep indexes inside the store and validate a whole page before
mutating them. Worker job snapshots omit those indexes.

Commit partial progress as waiting and release the lease while keeping the
bucket write fence. Resume with a new claim, including after process restart.
Process at most ten pages per sweep and use the existing 45-second scan budget,
1,000-page bound and one-hour job deadline. Transport/malformed-page failures
keep committed progress and all reservations. Cancellation/deadline cleanup
removes staging rows without rebasing quota. A complete native scan atomically
sets a sticky all-version baseline, resets conservative storage grants, records
an inventory sample and finishes the job. It leaves monthly authorization,
provider-attempt and authoritative billing ledgers intact.

After native versions are observed and before that baseline exists, decline new
writes. Once inventory scope is all_versions, each new tracked PUT/application
upload/copy reserves its full bytes and one retained entry even for the same
key. Journal native reservations per attempt instead of updating per-key maxima.
Tracked multipart completion replaces its part reservation during admission
and adds a full new version grant; reservations stay conservative until durable
completion. Legacy proxy writes, untracked completion and direct signed PUTs
remain declined in this mode until replay-safe admission is implemented.

Periodic refreshes enqueue/resume native capacity jobs instead of current-object
listing and stamp the existing inventory-attempt cadence before requesting a
job, so blocked legacy grants cannot create a new job every worker sweep. Customer API/OpenAPI/Go clients and CLI report scope and scanned
pages/bytes/entries, with no native identities. `capacity_keys` counts retained
data versions and delete markers for buckets with an all-version baseline.

Upgrade all gateways before using native capacity reconciliation; an old gateway
can still dispatch untracked DELETE and cannot participate in the native scan
fence. Managed buckets must have no independent writers, lifecycle removals or
replication during inventory. This increment does not qualify concurrent external
mutations.

Database triggers preserve sticky scope, reject unverified legacy rebases,
block legacy/current observations after native detection, and fence both native
journals and per-key grants during active jobs. Rollback refuses to destroy an
all-version baseline or active native inventory.

## Feature boundary

This enables accounting for versions already retained by a managed S3 backend.
It does not enable bucket versioning or offer public version IDs, version I/O,
version listing/deletion or restore. Current-object single/bulk DELETE returns
NotImplemented after native observation or all-version activation, because it
can create an additional marker without durable marker admission. Marker
admission and version-specific deletion are the next part of public versioning.
GCS, direct write replay safety, automatic proof retention on unversioned
backends, retention/replication and coordinated account deletion remain in the
[S3 implementation ledger](../s3-implementation-gaps.md).

## Validation

Local AWS SDK → branded gateway → S3 adapter → HTTP fixture → PostgreSQL tests
cover retained data/markers, later-page failure, restart with committed cursor,
partial scan fencing, overwrite quota, blocked unadmitted DELETE/bulk DELETE,
customer progress reads, periodic native refresh and exact provider-attempt
metering. Provider tests cover encoded/paired markers, null versions, Unicode
marker bytes, malformed pages, normalized failures and single-attempt dispatch.
Memory/PostgreSQL tests cover duplicate identities, cursor cycles, stale leases,
page rollback, cancellation/deadline staging cleanup, native admission and
tracked multipart overwrites. Migration tests exercise bounds, rolling-worker
fences, tenant separation and guarded down/up. No real provider tests are required.

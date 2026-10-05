# ADR-571 · Retain pending write proof and finish owned bucket cleanup

- **Status:** Accepted
- **Date:** 2026-10-04
- **Amends:** ADR-533, ADR-539, ADR-548 and ADR-564

## Context

An unversioned native object carries the private receipt used to recover an
acknowledgment lost after PUT. Admitting another writer for the same key can
replace that proof before recovery observes it. Historical version recovery
helps versioned buckets, but cannot reconstruct evidence already removed by
an unversioned overwrite. Developer/clone cleanup previously stopped at native
versioning or Object Lock, and the account grace sweep had no provider cleanup
coordinator. Metadata cascades correctly refused active buckets, leaving these
accounts unable to finish deletion.

## Decision

Pending write admissions own their key until settlement persists the receipt
and accounting result. Account-serialized admission rejects another PUT, copy,
route, legacy URL authorization or multipart completion on the same key. Other
keys and reads remain available. Fixed multipart completion reuses its own
admission. Existing deletion/capacity/configuration/bucket guards keep pending
evidence out of cleanup. PostgreSQL triggers preserve key custody for older
admission/grant writers; migration deployment does not retroactively resolve
already-dispatched competing requests.

Reuse the durable bucket deletion journal for recursive developer/clone and
expired-account cleanup. Claim deletion only after accepted writes, live
multipart uploads and configuration/deletion jobs drain. Keep the bucket sealed
through every retry, including `BucketNotEmpty`. Perform at most 100 current
objects or exact native versions per attempt. Every retry lists the first page:
confirmed removals disappear, and no saved cursor can skip a null/current
object changed by an old external capability. The same progress mechanism
survives process reconstruction and lost delete acknowledgments.

Enabled and Suspended buckets require native version inventory and exact
version deletion. Remove retained data versions and markers, including `null`
only within this permanently sealed cleanup. Protected data versions require
fresh legal-hold and retention reads. Active fixed/event retention and legal
holds persist a `protected` retry with a one-hour probe. Unreadable/malformed
policy fails closed. Never clear protection or send a governance bypass.
Markers do not carry Object Lock protection. Check each data version before
deleting it; previously removed, eligible versions remain valid partial progress
if a later protection read or provider call fails.

Only native bucket deletion completes the journal. The S3 adapter requires a
204 acknowledgment or a parsed 404 `NoSuchBucket`; an empty listing, another
404, malformed success or timeout cannot authorize metadata removal. Bucket
deletion uses one native attempt and the journal owns retries. Account grace
checks up to 20 account-owned buckets per pass, including buckets on app
tombstones. Expired accounts remain owned until every bucket is confirmed gone;
the existing cascade guard remains authoritative. Inactive accounts cannot
reserve new buckets, including through older SQL writers. Reservation and
account cascade use the account-before-app/bucket lock order. Restore remains
available during grace and is rejected after expiry.

The protocol follows [DeleteBucket](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucket.html),
[DeleteObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObject.html)
and [Object Lock considerations](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lock-managing.html).

## Limits and consequences

`ObjectOwnedCleanupBatchSize=100` and `ObjectOwnedCleanupBucketBatch=20` live in
`pkg/api/limits.go`. Continuation retries use 15 seconds; protection probes use
one hour. Existing 45-second provider operation deadlines and two-minute bucket
leases apply. Slow protection reads can make partial progress across attempts.

An uncertain write can fence its key indefinitely if exact positive proof never
appears. Absence and elapsed time still cannot prove failure. Legacy native URLs
issued before custody, out-of-band backend writes/deletes and provider lifecycle
rules cannot be retroactively fenced. These cases retain reservations and need
operator resolution; the implementation does not manufacture durable evidence.
Ordinary unversioned/Suspended/null deletion recovery retains its existing
conservative contract. Sealed recursive cleanup can repeat desired deletions
because it never restores the physical bucket for future writes. Physical names
must never be recreated out of band while a cleanup journal is active.

New customer Object Lock enrollment stays disabled. This decision qualifies
protected cleanup through local protocol fixtures; owned per-version management
and protection snapshots still belong to their separate implementation scope.
No live provider qualification is required for this work.

## Validation

Memory/PostgreSQL tests cover same-key admission races, independent keys,
legacy grants, multipart completion and settlement release. Gateway PUT tests
keep a lost-response receipt pending while rejecting overwrites, and PostgreSQL
upload recovery observes the original committed receipt. Raw SQL and guarded
rollback/reapply tests cover older writers. Local native HTTP -> S3 adapter ->
bucket worker -> account grace tests cover retention, legal hold, exact versions,
nulls/markers, multi-batch cleanup, lost acknowledgment, restart and final cascade.
Native terminal-proof and malformed/protection boundary tests fail closed.

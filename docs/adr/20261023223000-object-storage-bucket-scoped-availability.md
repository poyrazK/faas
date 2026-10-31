# ADR: Bucket-scoped object storage availability

Date: 2026-10-23
Status: Accepted
Related: [ADR-156](156-object-storage-accounting.md), [ADR-545](545-durable-bucket-versioning-configuration.md), [ADR-547](547-durable-s3-mutable-deletion.md), [ADR-627](627-prospective-gateway-storage-safety-accounting.md), [ADR-1320](1320-object-storage-plan-allowances-ceilings-and-overage-cap.md)

## Context

A GA review of managed object storage, together with findings from the
2026-10-12 production test (hunt #9), showed several ways that one bucket, or
someone outside the account, could make all of an account's storage
unavailable:

1. **One stale bucket blocked the whole account.** If any bucket's inventory
   was older than 900 s, the account's usage was not fresh, so every write and
   every gateway read on every bucket failed with 503 (H9-S142/S159, H10-129).
2. **The inventory sweep could not keep up.** It scanned 10 buckets a minute,
   one at a time. Past about 150 ready buckets on the fleet, buckets would
   miss the 900 s window and their accounts would lock.
3. **Inventory listings counted against the customer.** Every page of the
   periodic sweep incremented the same per-bucket request counter that is
   checked against `MaxMonthlyRequests`. An idle bucket spent 3,000–9,000
   requests a month that the customer never made.
4. **Reading versioning status fenced the bucket.** GCS reports a bucket that
   was never versioned as `Suspended`. The first observation of that status,
   whether from a read-only `GetBucketVersioning` or the first delete, started
   the 15-minute propagation fence. That froze the bucket's writes and deletes,
   and through (1) its whole account (H9-S135/S158).
5. **Concurrent deletes failed.** A bucket admits one deletion at a time
   (ADR-547), and every other deletion was refused at once (H9-S156).
6. **Conflicts looked like provider outages.** The gateway reported
   `state.ErrConflict` as `503 ServiceUnavailable "could not reach this
   bucket's storage placement"`.
7. **Public mounts let anyone exhaust the account's budget.** Anonymous reads
   drew from the account's monthly request, authorization and egress ceilings,
   with no per-bucket limit. 780 GETs in 79 s blocked the account's storage for
   the rest of the month (H9-S137).
8. **Deleted public objects stayed online.** Public mounts sent
   `public, max-age=31536000, immutable` on mutable keys, and nothing purges
   the CDN, so a deleted file stayed public for up to a year (H9-S144).
9. **One tenant could take every upload slot.** s3-gatewayd stages proxied
   PUTs, parts and part copies through a small pool shared by all tenants
   (4 by default), so a single `aws s3 sync` left everyone else with
   `SlowDown`.

## Decision

1. **Inventory freshness is per bucket.** Admission and gateway reservations
   require a fresh inventory of the *target* bucket only. Account-wide inputs
   stay account-wide: policy validity, provider reports and gateway metering
   coverage. A stale bucket still counts toward account totals with its
   conservative capacity: the larger of its last observation and baseline
   plus grants, plus open multipart reservations. `SummarizeObjectUsage`
   still reports `Fresh: false` while any inventory is stale, so the usage API
   and estimates are unchanged.
2. **The sweep drains due work concurrently.** apid claims due buckets in
   batches of `ObjectStorageInventorySweepBatch` (50) and scans up to
   `ObjectStorageInventorySweepConcurrency` (8) at once. It starts new batches
   until no bucket is due or `ObjectStorageInventorySweepBudget` (40 s) has
   passed. If a bucket comes back twice in one sweep, its claim did not
   advance, and the sweep stops instead of spinning.
3. **Periodic inventory is platform maintenance.** Sweep listings no longer
   count toward the account's request meter. They are counted in
   `faas_object_storage_inventory_provider_requests_total`. Provider requests
   that follow customer work still count: recovery, versioning, deletion and
   capacity reconciliation.
4. **A never-versioned baseline does not fence.** The fence still applies
   whenever a status may still be propagating: a change Gregale dispatched, a
   status that differs from an earlier observation, or a first observation of
   `Enabled`. The exception is a first `Suspended` observation on a bucket
   whose versioning Gregale never observed or changed. That observation still
   requires all-version accounting, but its propagation instant is the
   observation time, which has already passed. The schema requires every
   versions-required row to carry `propagation_until`, so no migration is
   needed. The bucket goes through the all-version inventory at once instead
   of waiting 15 minutes.
5. **Short contention is busy, and deletions wait it out.** A deletion
   refused only because other admitted work is settling (another deletion,
   version protection or a tracked write) returns
   `errors.Join(ErrConflict, ErrObjectBucketBusy)`. `DeletionService`
   retries busy admissions with jittered backoff, from
   `ObjectDeletionAdmissionPoll` up to `ObjectDeletionAdmissionMaxPoll`, for
   at most `ObjectDeletionAdmissionWait` (15 s). Concurrent deletes of
   different keys therefore queue. ADR-547's one active deletion per bucket,
   its unique index and its trigger fences are unchanged. Writes fenced by a
   deletion keep their `409 OperationAborted`: an uncertain deletion can hold
   that fence for much longer than a retry window.
6. **Contention has honest status codes.** In the S3 gateway, a deletion
   still busy after the wait answers `503 SlowDown` with `Retry-After: 1`,
   which SDKs retry with backoff. Any other conflict answers
   `409 OperationAborted`, for example an open multipart upload, a versioning
   transition or an all-version inventory in progress. Real provider failures
   keep `503 ServiceUnavailable`.
7. **Anonymous reads get a share and a rate limit.** Public-mount reads are
   admitted against `PublicObjectReadCeilingPercent` (75%) of the account's
   monthly request, egress, authorization and cost ceilings, after plan
   tightening. Usage is shared, so private access always keeps at least 25%.
   Each gatewayd-public process also allows at most
   `PublicObjectReadRatePerBucket` (100/s, burst 200) per bucket, and tracks
   at most `MaxPublicObjectReadLimiters` buckets. A used-up share answers 429
   rather than a misleading 503.
8. **Public objects revalidate.** Public reads send
   `public, max-age=300, must-revalidate`
   (`PublicObjectReadMaxAgeSeconds`). A delete or overwrite therefore reaches
   CDN and browser caches within five minutes. No CDN purge integration is
   added.

9. **Upload slots are shared fairly.** One account may hold at most
   `ObjectUploadAccountSlotSharePercent` (50%) of the gateway's staged-transfer
   slots, and always at least one. Past that share the account gets
   `SlowDown`, while other accounts keep the rest of the pool.

## Not decided here

- **Per-key deletion.** A bucket still runs one deletion at a time, and any
  open multipart upload still blocks its deletions for up to 24 h (H9-S138).
  Relaxing this means rewriting ADR-547's bucket-wide fence, which lives in a
  unique index and in trigger fences on seven tables, so it needs its own ADR.
- **Retargeting a versioning transition.** A request made while the baseline
  transition from (4) runs still gets 409 (H9-S157). The database forbids
  changing an active transition's target. The window is now seconds, not
  15 minutes.

## Compatibility

No schema change. Releases N and N-1 can run side by side. An N-1 apid still
counts its own sweep listings, which only makes it stricter. N-1 deletion and
write admission still refuse at once on contention, and their errors still
satisfy `errors.Is(err, ErrConflict)`.

## Validation

- `TestStaleInventoryOnlyBlocksItsOwnBucket` and
  `TestGatewayReadsIgnoreAnotherBucketsStaleInventory` cover both accounting
  modes.
- `TestVersioningBaselineObservation{Mem,PG}` checks that the baseline does
  not fence, while an `Enabled` observation and an observation after a
  requested change still do.
- `TestObjectDeletionBusy{Mem,PG}` and
  `TestDeletionAdmissionWaitsOutBusyBuckets` cover the busy error and the
  bounded wait.
- `TestAnonymousReadPolicyKeepsPrivateHeadroom`,
  `TestPublicReadLimiterBoundsEachBucket`, `TestPublicReadRefusalStatus` and
  `TestPublicCacheControlRevalidates` cover public reads.
- `TestInventorySweepDrainsEveryDueBucket` sweeps 157 buckets in one tick,
  under the race detector, without charging any listing to an account.
- `TestPutSlotsKeepRoomForOtherAccounts` covers the upload slot share.

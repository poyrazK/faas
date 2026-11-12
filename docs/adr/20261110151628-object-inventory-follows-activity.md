# ADR: Object inventory follows bucket activity, and the sweep keeps its workers busy

Date: 2026-11-10
Status: Accepted (2026-11-10)
Amends: [ADR 20261023223000](20261023223000-object-storage-bucket-scoped-availability.md) (the 900 s window and the batched sweep), [ADR-20261030100835](20261030100835-object-storage-resumable-inventory.md) (when an unfinished listing is due)
Related: [ADR-627](627-prospective-gateway-storage-safety-accounting.md), [ADR-20261024132844](20261024132844-gateway-metered-object-storage-billing.md), [ADR-20261029174627](20261029174627-object-storage-backend-pools.md), [ADR-20261031122757](20261031122757-s3-gateway-on-compute-nodes.md)

## Context

Admission refuses a bucket's reads and writes once its inventory is older
than 900 s, or older than an hour while a resumable listing advances. One
loop in apid keeps every bucket on the platform inside that window: 8
workers, batches of 50, no new batch after 40 s, one 45 s attempt per bucket
per sweep, then a full minute's wait.

[The measurement](../ops/evidence/20261110-object-inventory-capacity/REPORT.md)
ran that loop against PostgreSQL and a lister with OVH-like page latency
(100–300 ms, as ADR-20261030100835 recorded):

- **Small buckets.** One sweep refreshes 2,650 / 1,350 / 850 one-page buckets
  at 100 / 200 / 300 ms per page, with the listers about 80% busy. With the
  minute's wait, that keeps about 21,000 / 10,800 / 6,800 buckets inside
  900 s. At 200 ms that is 11 OVH projects of 950 buckets.
- **Large buckets collapse it.** A batch waits for its slowest bucket. With
  16 buckets of 500,000 objects among 2,000 small ones, the listers were
  21–30% busy and a sweep refreshed 150–200 small buckets instead of 1,350.
  In steady state an unfinished listing keeps its first attempt time, so it
  leads every batch. With 8 or more such listings in progress, each sweep
  refreshes at most 50 − *k* small buckets, and a few hundred small buckets
  already go stale.
- **Objects.** Every account may store 1,000,000 objects. A few dozen accounts
  near that limit exceed what the sweep can list in time. Every bucket then
  answers `503`, including those of accounts that did nothing.
- **Cost to the provider.** Every bucket is re-listed every 5–15 minutes
  whether or not it changed: 100–300 listing requests per bucket per day.

Most of that work is unnecessary. The window fails closed because an
inventory is what *lowers* a bucket's capacity and what catches changes
admission did not see. Between listings, every admitted write already raises
the bucket's capacity bound in the transaction that admits it: its grant,
multipart reservation or deletion journal. Under gateway-safety accounting,
production's mode, the registry allows only the `proxied` transfer profile,
so every customer byte passes through admission. A bucket with no admitted
mutation since its last listing started holds exactly what that listing
counted. A new listing can only find a change that bypassed Gregale. The code
already relies on this for a bucket whose versioning or Object Lock change is
in flight: "a held bucket cannot grow, so a past inventory holds"
(`objectBucketInventoryStale`).

## Decision

### 1. The sweep keeps its workers busy

- **No batch barrier.** Workers draw due buckets from one queue. A worker that
  finishes claims the next due bucket at once instead of waiting for the rest
  of its batch. After `ObjectStorageInventorySweepBudget` (40 s) the sweep
  starts no long listing. Short buckets fill the time the long listings in
  flight still take (under their own 45 s timeout), so the sweep never
  outlasts its longest listing and its workers do not idle meanwhile.
- **No idle minute while work is due.** A sweep that stopped on its budget
  with buckets still due is followed by the next one immediately. The loop
  waits its minute, or for `InventoryWake`, only when nothing is due.
- **Long listings take at most half the workers.** At most
  `ObjectStorageInventoryLongListingWorkers` (4) attempts at a time list a
  long bucket, so one-page buckets keep their cadence beside them. A listing
  is long when it continues an unfinished listing, or when the bucket's last
  inventory counted more than `ObjectStorageInventoryLongListingKeys`
  (100,000) keys. Without the second rule the first 45 s attempt of every
  large bucket could still take all eight workers. Continuing listings are
  still due on the next sweep, as ADR-20261030100835 decided, and a sweep
  hands out each bucket at most once.

### 2. A quiet bucket's inventory stays fresh for a day

- **Every bucket records when its contents last changed.**
  `object_storage_bucket_usage.mutated_at` is set in the same transaction as
  every admission that can change what a bucket holds or its capacity:
  - write admission (gateway PUT and copy, signed-URL and upload-route PUT,
    multipart session create, part admission and completion);
  - deletion admission (single, bulk, version and lifecycle expiry);
  - capacity reconciliation, versioning and Object Lock configuration;
  - fixture and restore writes.

  Write admission already updates that row for its grant, so most paths gain
  no statement. A contract test lists every writer of grants, multipart
  reservations and deletion journals, and fails when one does not set
  `mutated_at`.
- **Quiet means nothing was admitted since the last listing began.** Under
  gateway-safety accounting, a bucket whose `mutated_at` is older than the
  start of its published listing is quiet. A quiet bucket's inventory stays
  fresh for `ObjectInventoryQuietMaxAge` (24 h) instead of 900 s, and it is due
  for a verification listing once per that period. An active bucket stays due
  5 minutes after its last attempt and fresh for 900 s, as today.
- **Verification measures drift.** A verification listing of a quiet bucket
  that counts different bytes or keys than its observation increments
  `faas_object_storage_inventory_drift_total{direction}` and logs the bucket.
  On a quiet bucket, drift means something wrote around admission, for
  example root on a compute node using the gateway credential
  (ADR-20261031122757 §4) or an operator in the provider console.
- **Unchanged where admission is not the only door.** Provider-report
  accounting and owned storage nodes keep the 900 s window for every bucket.
- **Shadow first.** The `object_inventory_quiet` runtime switch has three
  values:
  - `off`: no change;
  - `shadow`: classify buckets and run verification listings, but still
    require 900 s everywhere;
  - `on`: apply the 24 h window to quiet buckets.

  It moves to `on` only after a shadow week with zero drift on quiet buckets.

### 3. Backlog is visible before it refuses traffic

- `faas_object_storage_inventory_due_buckets` reports the due backlog.
- `faas_object_storage_inventory_oldest_observation_age_seconds{class}`
  reports the oldest observation age, with `class` `active` or `quiet`.
- `FaasObjectStorageInventoryBacklog` warns when the oldest active
  observation passes 600 s for 5 minutes, before admission starts refusing
  at 900 s.

## Steps

1. Sweep scheduling (§1): code only. Re-run `TestInventorySweepCapacity`.
   Pass means the listers stay at least 75% busy in the mixed scenario, and a
   small-only cycle takes under 50 s.
2. Metrics and alert (§3).
3. `mutated_at`:
   - an expand migration;
   - set by every mutation path;
   - the contract test.
4. Quiet freshness and verification cadence behind `object_inventory_quiet`.
   Run in `shadow` for a week, then `on` with the owner's go-ahead.
5. Re-measure and record the result here.

## Step 1 result (2026-11-10)

`TestInventorySweepCapacity` on the new sweep, at 200 ms per page
([evidence](../ops/evidence/20261110-object-inventory-capacity/REPORT.md#after-step-1-2026-11-10)):

- **Small buckets:** 1,505 per 40 s sweep, listers 93% busy. The next sweep
  starts at once, so about 32,000 one-page buckets stay inside 900 s, up from
  about 10,800.
- **Steady state with 16 large buckets** (listed before, 500,000 keys each):
  795–1,205 small buckets per sweep with the listers 94–96% busy, and 4
  large listings advancing each sweep. Main's sweep, measured the same way,
  refreshed 49–99 at 15–17% busy and then waited a minute.
- **Pass criteria.** Small-only cycle under 50 s: met (about 40 s). Listers at
  least 75% busy with large listings: met in steady state (94–96%). The
  original mixed scenario, whose 16 buckets were full but never listed,
  stayed at 68–82%. A bucket with no inventory has unknown size, so its first
  45 s listing takes a short worker. In production that happens only to a
  bucket that is new (and empty) or restored full.

## Step 5 result (2026-11-11)

`TestInventorySweepCapacity/quiet-200ms` ran with the switch on. It had
22,000 one-page buckets at 200 ms per page: 20,000 quiet, 1,000 active and
1,000 quiet ones due for verification
([evidence](../ops/evidence/20261110-object-inventory-capacity/REPORT.md#after-step-4-2026-11-11)).

- 2,000 buckets were due, and none of the 20,000 quiet ones was listed. Two
  sweeps (55 s) listed every active bucket and every verification, the
  active ones in the first sweep.
- The sweep's rate is unchanged, about 37 one-page buckets a second. With
  quiet on, the work per 900 s is the active buckets plus 1/92 of the quiet
  ones. That is in line with the Consequences below: about 30,000 active
  buckets, or a few million quiet ones verified daily.
- Measuring found one fix. The due query first ordered verifications
  (attempted 23 h before) ahead of active buckets. It now orders each bucket
  by how close it is to its own limit.
- The next limit is the due query itself. It reads every ready bucket: 96 ms
  for 22,000 with quiet on, linear in the bucket count. A due-time column
  with a partial index should come before a few hundred thousand buckets.
  That is not part of this ADR.

## Step 3 notes (2026-11-11)

How §2's `mutated_at` is kept, refined during implementation. None of these
choices changes what quiet means.

- **One stamp site per journal, in Postgres.** `mutated_at` lives in its own
  table, `object_bucket_activity`, and triggers stamp it. They fire on the
  journals every content change already writes in its own transaction:
  - `object_bucket_mutations`, insert and delete. Every provider write,
    copy, multipart step, upload route and clone grant records a source
    writer there before provider IO and clears it after a validated
    acknowledgement (ADR-590).
  - `object_deletions`, insert and state change.
  - `object_bucket_versioning`, `object_bucket_object_lock` and
    `object_storage_capacity_reconciliations`.

  A writer that forgets to stamp would have to skip the journal, which
  clones already depend on. Stamping in the database also covers the
  previous release's daemons from the moment the migration runs. The contract
  test (`TestObjectBucketActivityTriggersPG`) lists the stamped tables;
  `TestObjectBucketActivityMemParity` holds the MemStore to the same writes.
- **A write is covered from admission to acknowledgement.** The writer is
  stamped when it begins and when it settles. A listing published while a
  writer or deletion of its bucket is still outstanding also stamps the
  bucket. Such a write may land after the listing started, or never settle
  if its reply was lost.
- **Coarse stamps.** A bucket's row is rewritten at most once per
  `ObjectInventoryMutationStampGranularity` (30 s). A burst of PUTs costs one
  row update, not two per request, and a stamp within the granularity
  takes no row lock. A bucket is quiet only when `mutated_at` is older than
  its listing's start by more than the granularity.
- **`active_since`** records the first stamp after a bucket was last quiet.
  Step 4 anchors an active bucket's 900 s window there. Otherwise a quiet
  bucket observed hours ago would answer `503` from its second write until
  the sweep listed it again.
- **Rollout.** Every bucket that exists when the migration runs starts at the
  migration's time. A bucket created later has no row until its first
  mutation.

## Step 4 notes (2026-11-11)

- **Where the switch is read.** apid validates and applies
  `object_inventory_quiet` like any hot runtime setting. Postgres reads the
  applied value (`object_inventory_quiet_mode()`) in the same query that
  loads bucket usage for admission and the sweep. Every daemon that admits
  object storage traffic sees one mode without a watcher of its own. A
  pending change, or a value apid refused, reads as `off`.
- **Gateway safety only.** Admission applies the quiet window only under
  gateway-safety accounting. The sweep passes its accounting into the due
  and backlog queries, so a provider-report deployment keeps the 5-minute
  cadence whatever the switch says. Owned storage nodes are not part of this
  window: they audit their own inventory.
- **Cadence.** A quiet bucket is due once its observation is
  `ObjectInventoryQuietVerifyAfter` (23 h) old. That leaves an hour for a
  long listing before the 24 h limit. A listing in progress stays due.
- **An active bucket's window** starts at `active_since` when the bucket was
  quiet on its observation and turned active within 24 h of it. Up to that
  moment the observation was exact. Without this, a write to a bucket quiet
  for hours would make its next request answer `503`. The backlog gauge's
  `class="active"` counts from the same anchor; `class="quiet"` shows the
  oldest quiet observation, and `FaasObjectStorageInventoryQuietBacklog`
  warns past 23.5 h.
- **Verification is every listing of a quiet bucket.** In `shadow` the
  cadence is unchanged, so each 5-minute listing of a quiet bucket is a
  verification: the shadow week measures drift at full rate. Publication
  reports drift (`FinishObjectInventoryScanVerified`), apid counts it and
  logs the bucket, and `FaasObjectStorageInventoryDrift` warns.
- **Leaving `on` is not free.** Returning to `shadow` or `off` holds every
  bucket to 900 s at once, so each quiet bucket listed longer ago refuses
  transfers until the sweep reaches it. The runbook says when that is worth
  it.

## Consequences

- Listing work follows activity. Every 15 minutes, only buckets that changed
  are listed; every bucket is listed once a day. These are estimates from the
  200 ms measurement after step 1, at about 34 attempts per second:
  - about 30,000 active buckets can stay inside the 900 s window;
  - the daily verification covers about 2.9M one-page buckets, or about 1.7B
    objects in large buckets with 4 continuing workers.

  Before this, the limit was roughly 10,800 small buckets with no large ones.
- Provider listing requests for an untouched bucket drop from 100–300 a day
  to one.
- A write that bypasses admission shows in a quiet bucket's capacity and
  storage bill up to 24 h later instead of 15 minutes. The drift counter
  makes it visible. Admitted writes still raise capacity at once, and the
  capacity they reserve is unchanged.
- Deletions on an active bucket still free capacity at its next listing, as
  today.
- Every content-changing admission must set `mutated_at`. A path that forgets
  makes its bucket look quiet while it changes; the contract test and the
  shadow week's drift counter are the guards.
- One apid still runs the sweep. Its claims are exclusive, so sharding it
  across apid processes stays available as the next step if active buckets
  outgrow one sweeper.

## Alternatives considered

- **More workers and a shorter wait only (§1 alone).** It gains about 2–3×
  and keeps the rest. Work and provider requests still grow with every stored
  bucket and object, whether or not anything changed.
- **Shard the sweep across apid processes now.** It is linear in processes and
  linear in stored objects, still at 100–300 listings per bucket per day. It
  is worth adding on top of this ADR, not instead of it.
- **Provider inventory reports (S3 Inventory).** They are not offered by
  every backend Gregale supports, and they lag by a day. Where available,
  they could replace the daily verification listing later.
- **Meter from receipts and stop listing.** Deletions and overwrites need
  per-key sizes to lower capacity, and nothing would catch drift. The listing
  stays the authority; this ADR only stops repeating it when nothing changed.
- **A longer window for every bucket.** An active bucket's capacity would
  keep its deleted bytes and its storage bill would lag its growth for longer,
  with no reduction in risk for buckets that are actually changing.

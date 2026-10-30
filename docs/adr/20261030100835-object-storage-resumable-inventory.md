# ADR: Object storage inventory resumes across attempts

Date: 2026-10-30
Status: Accepted
Related: [ADR-156](156-object-storage-accounting.md), [ADR-627](627-prospective-gateway-storage-safety-accounting.md), [ADR 20261023223000](20261023223000-object-storage-bucket-scoped-availability.md), [ADR-20261025115958](20261025115958-object-storage-per-key-deletion.md), [ADR-20261024132844](20261024132844-gateway-metered-object-storage-billing.md)

## Context

apid keeps an inventory of every bucket: its listed bytes and keys, and when
they were listed. Admission refuses a bucket's transfers once its inventory is
older than 900 s (ADR 20261023223000), and gateway storage metering bills the
bucket's last listed bytes (ADR-20261024132844).

An inventory is one listing of the whole bucket, 1,000 keys per provider page,
inside one 45 s attempt (`ObjectCapacityInventoryTimeout`). A failed attempt
keeps nothing, and the next one starts again from the first key five minutes
later. Provider listing pages take 100–300 ms on OVH and GCS, so a bucket
above roughly 150,000–450,000 objects can never finish a listing. Its
inventory goes stale 900 s after the last success, and from then on the
bucket refuses its own uploads and reads with `503` until objects are
deleted. Nothing else warns the customer: the account's key ceiling allows up
to 1,000,000 keys (`ObjectStoragePolicy.Valid`), and the guide promises no
lower number. Metering keeps billing the last bytes it saw.

## Decision

1. **A listing is checkpointed.** A bucket's listing in progress is a row in
   a new table, `object_storage_inventory_scans`: when it started, the last
   key listed, the bytes, keys and pages so far, when it last advanced, and
   whether it has reached the end. An attempt lists from the checkpoint's
   last key (S3 `StartAfter`, GCS `startOffset`) for at most 45 s, then
   records its progress and releases its claim. Every page is validated as
   before: keys strictly ascending, including across attempts, valid keys and
   sizes, no cursor cycle. One listing still spans at most
   `ObjectStorageInventoryMaxPages` pages (1,000,000 keys); past that it is
   discarded, as today.
2. **A completed listing is published as of its start.** The listing that
   reaches the end publishes its totals with the time the listing started as
   the observation time. A listing spread over minutes is not a snapshot;
   each key is counted once, with the size it had when listed, so its error
   is the churn during the listing, as with today's 45 s listing. A
   checkpoint that started before the bucket's current observation, or more
   than `ObjectInventoryScanMaxAge` (1 h) ago, is discarded and listing starts
   again.
3. **Only the start and the publication make deletions wait.** Inventory
   publication still refuses while any deletion in the bucket is unsettled
   (ADR-547), so the attempt that starts a listing, like today's, claims the
   bucket with a fresh attempt time (new deletions are busy for
   `ObjectInventoryDeletionYield`) and drains earlier deletions before it
   lists. Most buckets finish in that attempt and publish exactly as before.
   An attempt that continues a listing keeps the listing's attempt time, so
   deletions do not wait for it: listing needs no quiet bucket, only
   publication does. If a continuing attempt reaches the end while a deletion
   is unsettled, it records the listing as complete; the next attempt claims
   with a fresh attempt time, drains deletions and publishes it.
4. **An unfinished listing is due at once.** A bucket with an incomplete
   listing is due on the next sweep rather than five minutes after its last
   attempt. It still gets one attempt per sweep, and the sweep's batching,
   concurrency and budget are unchanged.
5. **A progressing listing keeps its bucket available.** A bucket whose
   inventory is older than 900 s stays fresh while its listing is progressing,
   meaning it advanced within `ObjectInventoryScanProgressWindow` (5 min),
   and its last published observation is younger than
   `ObjectInventoryScanMaxAge` (1 h). The bytes and keys listed so far also
   count toward the bucket's capacity: it is the largest of its observation,
   its listing so far and its baseline plus grants, plus open multipart
   reservations. A listing that stalls, for example because the provider
   fails every page, stops counting after 5 min and the 900 s rule applies.
6. **Outcomes.** `faas_object_storage_inventory_scans_total` gains
   `outcome="partial"` for an attempt that recorded progress without
   finishing. An attempt that fails after listing some pages keeps them and
   counts as `failed`.

Owned-node inventory (ADR-1545) and owned usage counters keep their single
attempt: their cursors expire with the catalog revision. A provider without
`StartAfter` support also lists in one attempt, as today.

## Consequences

- A bucket of up to 1,000,000 objects keeps a fresh inventory. At 300 ms per
  page, a full listing is about 300 s of provider time; at one 45 s attempt
  per sweep it finishes in about 12 minutes and keeps the bucket available
  meanwhile.
- A large bucket's admission may rest on an observation up to an hour old,
  plus what the current listing has seen. Accepted writes still count through
  grants. Bytes written to the provider outside the gateway are detected up
  to an hour late instead of 15 minutes.
- Metering bills the last published bytes, now refreshed for large buckets
  too.
- Each attempt writes one checkpoint row: one write per bucket per sweep
  while it lists, not one per page.

## Not decided here

- **Capacity reconciliation** (`POST …/capacity-reconciliations`) still lists
  in one 45 s attempt and retries for up to an hour while holding the bucket's
  write fence, and inventory waits for it. It needs its own resumable listing
  under that fence.
- **More than 1,000,000 keys** per bucket. The account key ceiling stays at
  the inventory's page budget.

## Compatibility

`object_storage_inventory_scans` is a new table (expand). Release N-1 never
reads it. N-1's claim sets a fresh attempt time and lists in one attempt; if
it publishes, its observation is newer than N's checkpoint, which N then
discards instead of publishing an older listing. N-1 admission ignores
listings in progress, so it can find a large bucket stale where N does not,
which is today's behaviour.

## Validation

- `TestResumeObjectInventory*`: listing stops at its deadline and resumes
  after the last key; keys out of order across attempts, oversized listings
  and cursor cycles fail; a provider error keeps earlier pages.
- `TestObjectInventoryScan{Mem,PG}`: start, continue and publish claims set
  the attempt time only when starting or publishing; a checkpoint older than
  the bucket's observation or than an hour is discarded; publication uses the
  listing's start and refuses while a deletion is unsettled; an unfinished
  listing is due at once.
- `TestObjectInventoryScanFreshness`: a progressing listing keeps its bucket
  fresh up to an hour and adds its counts to capacity; a stalled one does not.
- `TestInventorySweepResumesLargeBucket`: apid lists a bucket over several
  sweeps, deletions are not held by continuing attempts, and the bucket stays
  available until the listing publishes.

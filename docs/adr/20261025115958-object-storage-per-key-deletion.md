# ADR: Per-key object deletion fences

Date: 2026-10-25
Status: Accepted
Amends: [ADR-547](547-durable-s3-mutable-deletion.md) ("one active intent per bucket")
Related: [ADR 20261023223000](20261023223000-object-storage-bucket-scoped-availability.md)

## Context

ADR-547 journals every ordinary and null deletion before the provider sees
it, and lets a bucket hold one active deletion intent at a time. That intent
also fences every write admission, multipart initiation, key grant and copy in
the bucket, whatever key they touch, and deletion admission waits for every
pending write in the bucket to settle. ADR 20261023223000 made a refused
deletion wait up to 15 s instead of failing at once, but deletes in a bucket
still run one at a time:

- `aws s3 rm --recursive`, `rclone delete` and S3 `DeleteObjects` send many
  deletes at once. Each waits for the one before it, and a bulk request of
  1,000 keys takes 1,000 provider round trips in sequence. Clients see
  `SlowDown` once the queue is longer than the wait.
- A single delete blocks uploads of unrelated keys for its whole provider
  round trip, and an uncertain delete that keeps its fence blocks them until
  it settles.
- Inventory publication refuses while any deletion is active. A steady stream
  of deletes can starve a bucket's inventory past the 900 s freshness window,
  after which the bucket refuses writes.

The hazards ADR-547 guards against are per key: a retried null delete can
remove a later write of the same key, and an Enabled delete's baseline lists
the versions of one exact key. Versioning and Object Lock transitions,
capacity reconciliation and inventory replace bucket-wide state, so those
still need the whole bucket to be quiet.

## Decision

1. **One active deletion per key.** The unique index on active deletions
   becomes `(bucket_id, object_key)`. A second deletion of the same key is
   busy and waits like any busy deletion.
2. **A deletion waits only for its own key.** Admission is busy while the same
   key has a pending proxy write, an unfinished multipart upload or write, or
   another active deletion. Bucket-wide refusals stay: the write fence,
   pending version protection, versioning, Object Lock or capacity
   transitions, unsafe legacy grants, and a bucket that is not ready.
3. **Only a deletion of the same key fences a write.** Write admission,
   multipart initiation and key grants check their own key, and the database
   triggers enforce the same scope. A copy within a bucket is an ordinary write
   of its destination key.
4. **Bucket-wide work still waits for every deletion.** Versioning, Object
   Lock and encryption configuration, capacity reconciliation, version
   protection, inventory publication, cross-bucket copies and copy-source
   grant changes, the write fence and bucket deletion keep refusing while any
   deletion in the bucket is active.
5. **Deletions yield to inventory.** While a bucket's inventory claim is less
   than `api.ObjectInventoryDeletionYield` old, new deletions in that bucket
   are busy. After claiming, the inventory worker waits up to
   `api.ObjectInventoryDeletionDrain` for deletions admitted before the claim
   to settle, then lists. If they do not settle, or the scan fails, it releases
   the claim (`ReleaseObjectInventory`), so a stuck deletion costs other
   deletes one drain wait per inventory attempt, not a whole lease.
6. **Bulk deletes run concurrently.** S3 `DeleteObjects` runs up to
   `api.ObjectDeletionBulkConcurrency` entries at once. Entries keep their
   request-derived IDs and the response keeps request order.

## Consequences

- Deletes of different keys in one bucket overlap; only their admission and
  settlement transactions still serialize on the bucket's rows.
- Deletes and uploads of different keys no longer block each other.
- Under sustained deletes, a bucket's deletions are busy for at most the
  drain wait plus one scan (under a minute) per inventory attempt, and its
  inventory stays fresh.
- **Compatibility:** the migration replaces the unique index and the trigger
  function. migration-lint counts dropping the per-bucket index as a contract
  change, so a full release applies it. An N-1 apid still counts every active deletion in
  the bucket as pending and checks the bucket-wide fence before admitting a
  write, so a mixed fleet only loses concurrency. Rolling the migration back
  recreates the per-bucket index, which fails while two deletions in one
  bucket are active; wait for them to settle.

## Validation

- Memory and PostgreSQL store tests: deletions of two keys in one bucket are
  both admitted; a second deletion of the same key is busy; a write to another
  key is admitted while a deletion is active and a write to the same key is
  fenced; multipart initiation follows the same rule; versioning
  configuration still waits for every deletion; a fresh inventory claim makes
  deletions busy, and a released claim lets them through.
- The inventory worker waits for an active deletion before listing and
  releases its claim when the deletion does not settle.
- Gateway test: a bulk delete runs entries concurrently and returns them in
  request order.

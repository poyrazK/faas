# ADR: The inventory sweep reads its due buckets from an indexed schedule

Date: 2026-11-11
Status: Proposed
Amends: [ADR-20261110151628](20261110151628-object-inventory-follows-activity.md) (how the sweep finds due buckets; the due rules are unchanged)
Related: [ADR-20261030100835](20261030100835-object-storage-resumable-inventory.md)

## Context

apid's inventory queue refills from `ObjectInventoriesDue`: the buckets
that are due, in order of how close each is to its own limit, a page at a
time. The query decides what is due per bucket from four tables (the
bucket, its usage row, its activity row and its listing row) and sorts
the result, so every refill reads every ready bucket.

[Measured](../ops/evidence/20261110-object-inventory-capacity/REPORT.md)
after ADR-20261110151628 step 4: 96 ms per refill over 22,000 buckets with
`object_inventory_quiet` on and 23 ms with it off, linear in bucket count.
Quiet mode exists to let the platform hold many more buckets than the
sweep can list every 15 minutes; at 200,000 buckets each refill would
read all 200,000 to hand out 100.

An index cannot be built on that query as written. Whether a bucket is
quiet compares its activity row (`mutated_at`, written by the transaction
that admits each mutation) with its usage row (`observed_at`, written by
the sweep), and a btree index or its predicate covers one table.

## Decision

### 1. One schedule row per bucket, kept by triggers

`object_inventory_schedule` holds, per bucket, a copy of the four values
the due rules sort and split on:

| column | copy of | when no source row |
|---|---|---|
| `attempt_at` | `object_storage_bucket_usage.attempt_at` | `-infinity` |
| `observed_at` | `object_storage_bucket_usage.observed_at` | `-infinity` |
| `quiet_after` | `observed_at − 30 s` for a `current`-scope observation | `-infinity` |
| `mutated_at` | `object_bucket_activity.mutated_at` | `-infinity` |

A bucket is quiet exactly when `mutated_at < quiet_after`, the rule
ADR-20261110151628 §2 already applies (never observed, all-versions
scope and never mutated fall out of the `-infinity` defaults). Both sides
are columns of one row, so the comparison can be an index predicate.

Each copy is written by a trigger in the transaction that changes its
source, so it is never behind what a reader of the source tables sees:

- a row is inserted with the bucket (`object_buckets` insert), before any
  mutation, listing or activity can name it, and deleted when the bucket
  becomes `deleted`;
- `object_storage_bucket_usage` insert, or update of `attempt_at`,
  `observed_at` or `inventory_scope`, copies those;
- `object_bucket_activity` insert, or update of `mutated_at`, copies it.

The activity trigger adds one row write to a stamp, which happens at most
once per 30 s per bucket, and locks only the schedule row after the
activity row. Nothing else that locks the schedule row then waits on
another lock except a usage write's own transaction, so the only new lock
order is activity → schedule and usage → schedule.

### 2. The due query reads candidates from three indexes

| index | rows | order |
|---|---|---|
| `object_inventory_schedule_attempt` | every bucket | `attempt_at` |
| `object_inventory_schedule_active` | not quiet | `attempt_at` |
| `object_inventory_schedule_quiet` | quiet | `observed_at` |

plus `object_storage_inventory_scans_open` (unfinished listings with a
page, by start time).

`ObjectInventoriesDue` takes, from each index, the first `limit` buckets
that are due by that index's class, and adds every bucket with an open
listing. The due set is the union of those classes (§3), and each class
is read in its own due order, so the union holds the first `limit` due
buckets in the overall order. The query then looks each candidate up by
key, applies the unchanged due predicate and order of ADR-20261110151628
to that candidate set only, and returns the first `limit`. (A join on the
candidate set let the planner hash it against every bucket at 22,000
buckets; a key lookup from an array cannot.)

A refill reads at most about 3 × `limit` schedule rows, plus the open
listings, plus whatever an index range holds that a filter drops: buckets
leased or skipped by this sweep, held by versioning or a capacity
reconciliation, or not `ready` (provisioning or being deleted). None of
those grows with the number of buckets.

### 3. The due rules are unchanged

With `object_inventory_quiet` on and gateway-safety accounting:

- an active bucket is due when its last attempt is more than 5 minutes old
  (`object_inventory_schedule_active`, by attempt);
- a quiet bucket is due when its observation is
  `ObjectInventoryQuietVerifyAfter` old and its last attempt more than
  5 minutes old (`object_inventory_schedule_quiet`, by observation; its
  rank is observation + `ObjectInventoryQuietMaxAge` − 900 s);
- a bucket with an unfinished listing is due at once
  (`object_storage_inventory_scans_open`).

Otherwise every bucket is due on the 5-minute retry
(`object_inventory_schedule_attempt`, by attempt) or at once with an
unfinished listing. In both modes a leased, skipped, held or not-`ready`
bucket is left out, and a never-listed bucket sorts first.

`ObjectInventoryBacklog` (once a minute) still reads every bucket; it is
a gauge, not on the sweep's path.

### 4. Rollout

One expand migration creates the table, indexes and triggers, then fills
the table: every bucket that is not `deleted` gets a row with
`mutated_at = now()` (every existing bucket is active until its next
listing, as ADR-20261110151628 step 3's backfill did), then copies each
usage row. A usage write racing the fill can leave a copy older than its
source; an older attempt or observation only makes the bucket look due
or active earlier, and its next claim or listing rewrites it. A
`mutated_at` copy is never older than its source.

The Go code is unchanged apart from the query; the in-memory store keeps
computing the same rules directly.

## Consequences

- A refill's cost depends on `limit` and on how many buckets are leased,
  skipped or held, not on how many buckets exist.
- Every activity stamp writes one more row, and every claim, listing
  publish and rebase one more row. Each such write moves the bucket's index
  entries, so a due range starts behind the dead entries of buckets claimed
  since the last vacuum; autovacuum's defaults cover the table, as they do
  the usage table it shadows.
- The 30 s stamp granularity is a literal in the schedule trigger as it is
  in `object_bucket_mark_mutated`; `ObjectInventoryMutationStampGranularity`
  names both.
- A version-inventory finish stamps the bucket and then rebases its usage
  row. An inventory attempt on the same bucket in that window waits on the
  schedule row while holding the usage row; Postgres aborts one of the
  two background transactions and both retry. Claims and listing
  publishes skip buckets under a capacity reconciliation, so this needs an
  inventory lease taken before the reconciliation started.

# Object inventory sweep capacity (2026-11-10)

Admission refuses a bucket's reads and writes once its inventory is older
than 900 s (ADR 20261023223000), or older than an hour while a resumable
listing advances (ADR-20261030100835). One loop in apid keeps every bucket on
the platform inside that window. This report measures how many buckets and
objects that loop can keep fresh, and why it falls well short of the
1,000,000 objects every account may store. It is the evidence for
[ADR-20261110151628](../../../adr/20261110151628-object-inventory-follows-activity.md).

## Setup

- **Code:** the real sweep, `reconcileObjectInventories` (8 workers, batches
  of 50, no new batch after 40 s, one 45 s attempt per bucket per sweep), on
  main `093e29c871`.
- **Store:** `PgStore` on a local PostgreSQL 16 (fsync on, sub-millisecond
  round trips, as apid has to the control-plane database).
- **Provider:** a lister that answers 1,000 keys per page after a fixed
  latency. ADR-20261030100835 recorded 100–300 ms per listing page on OVH and
  GCS. OVH itself was not measured: no OVH backend exists yet.
- **Buckets:** "small" buckets hold 3 keys (one page). "Large" buckets hold
  500 full pages (500,000 keys). All start ready and never inventoried, so
  every one is due.
- **Harness:** `TestInventorySweepCapacity`
  (`pkg/apid/object/inventory_capacity_pg_test.go`), skipped unless
  `FAAS_INVENTORY_CAPACITY=1`:

  ```bash
  FAAS_INVENTORY_CAPACITY=1 FAAS_PGTEST_TEMPLATE_DATABASE=1 DATABASE_URL=postgres://… \
    go test -run '^TestInventorySweepCapacity$' -v -timeout 30m ./pkg/apid/object
  ```

## Results

| Scenario | Page latency | Sweep | Buckets refreshed | Pages | Listers busy |
|---|---|---|---|---|---|
| 4,000 small | 100 ms | 40.2 s | 2,650 | 2,650 | 82% |
| 3,000 small | 200 ms | 40.2 s | 1,350 | 1,350 | 84% |
| 2,000 small | 300 ms | 41.0 s | 850 | 850 | 78% |
| 2,000 small + 16 large | 200 ms | 50.6 s | 199 + 1 partial | 422 | 21% |
| same, second sweep | 200 ms | 49.0 s | 148 + 2 partial | 594 | 30% |

The due query over 2,000–4,000 buckets took 6–11 ms. The database is not the
limit; provider latency and the sweep's scheduling are.

## What the numbers mean

**Small buckets.** After a sweep the loop waits a full minute
(`ticker.Reset` after each sweep), so one cycle is about 100 s. A bucket must
be re-listed within 900 s, which leaves about eight cycles:

| Page latency | Buckets per sweep | Buckets kept fresh (× 8) |
|---|---|---|
| 100 ms | 2,650 | ≈ 21,000 |
| 200 ms | 1,350 | ≈ 10,800 |
| 300 ms | 850 | ≈ 6,800 |

At 200 ms that is about 11 OVH projects at 950 buckets each, or about
1,100 apps using their default 10 buckets. The count holds only while every
bucket fits in one listing page.

**Large buckets collapse it.** A bucket whose listing needs more than one 45 s
attempt (about 225,000 objects at 200 ms) holds its worker for 45 s. A batch
does not start the next until all 50 of its buckets finish, so the other seven
workers go idle (21–30% busy above). With 16 large buckets, the sweep
refreshed about 150–200 small buckets instead of 1,350, a 7–9× drop.

In steady state it gets worse than measured here. An unfinished listing keeps
the attempt time of its first attempt, so it is the oldest due bucket and
leads every batch. With *k* large listings in progress, the first batch holds
them plus 50 − *k* small buckets and lasts ⌈*k*/8⌉ × 45 s. By then the 40 s
budget has passed, so the sweep ends after that batch:

- at most 50 − *k* small buckets are refreshed per sweep (34 for *k* = 16), so
  a few hundred small buckets already exceed the 900 s window;
- a large bucket stays available only while its listing advanced in the last
  5 minutes. Past about 45 large listings (⌈*k*/8⌉ × 45 s + 60 s > 300 s), the
  large buckets go stale too. Past 50, some get no attempt in a sweep at all.

**Objects.** One sweep lists at most 8 × 45 s ÷ page latency pages
(1,800 pages, 1.8M keys, at 200 ms), and only within one batch. Every
account may store 1,000,000 objects (`MaxObjectStorageAccountKeys`). A few
dozen accounts near that limit, spread over large buckets, exceed what one
apid can keep fresh. Every bucket on the platform then answers `503`.

**Cost to OVH.** Every bucket is re-listed every 5–15 minutes whether or not
anything changed. That is 100–300 listing requests per bucket per day even
for a bucket nobody touches. At 10,000 buckets that is 1–3M listing requests
a day, all of them platform maintenance.

## Limits of this measurement

- The provider latency is injected, not measured against OVH. The ranking of
  the bottlenecks does not depend on the exact value; the absolute numbers
  scale with 1 ÷ latency.
- One apid ran the sweep, as production does. The claims are exclusive, so
  several sweepers could share the work; production runs one.
- Native version inventories (all-version accounting) and owned storage nodes
  take other listing paths and were not measured.

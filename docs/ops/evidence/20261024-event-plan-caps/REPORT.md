# Event plan caps: counted receipt usage — 2026-10-24

ADR-20261023215834 raises the retained event budgets to 8,388,608 events on
Scale. Before it, every customer publish counted and summed the account's
retained receipts while it held the account's admission lock, so publishes to
one account ran one after another behind a scan of its whole history. The
branch keeps per-owner counts in `event_receipt_usage` with triggers on
`event_fanout_outbox`; admission reads that row, and the settled-receipt prune
walks it to find owners with a receipt past their plan's window.

Measured on `gregale-internal-test-1` (2 vCPU, PostgreSQL 16.15, a throwaway
cluster with the default `jit = on`; production runs `jit = off`),
2026-10-24. `bench.sql` loads into a fresh database made from the branch's
`schema.sql` and seeds one account with 1,048,576 settled receipts (today's
Scale budget, about 400 bytes charged each) and 200 accounts with 1,000 each
across the four plans: a 1,077 MB outbox. `bench.log` is the full output.

## Admission read

The read each publish makes under the account's admission lock, all pages in
shared buffers:

| | Runs |
|---|---|
| main: `count(*)`, `sum(customer_storage_bytes)` over the account's receipts | 1,008, 1,271, 766, 713 ms; 1,415 ms under `EXPLAIN ANALYZE` (parallel index-only scan, 8,140 buffers) |
| branch: the account's `event_receipt_usage` row | 0.61, 0.21, 0.17, 0.16 ms; 0.03 ms execution |

At today's Scale budget a full account could publish about one event a second
on main. The count grows with the budget, so at 8,388,608 it would be several
seconds per publish.

## Cost of keeping the counters

- Single-row customer inserts, each committed, 5,000 per run: 7,878 and
  8,096 ms with the counter triggers, 8,984 and 7,152 ms without (about
  1.6 ms each either way; the commit dominates).
- One prune batch, a 100-receipt delete rolled back: 15.5 and 9.7 ms with the
  statement-level trigger, 10.9 and 7.6 ms without.
- After the runs, the heavy account's row was short by exactly the 10,000
  receipts inserted with the triggers disabled.

## Prune owner walk

The candidate query walks every usage row, resolves the owner's plan and
probes the owner's retention index for an unheld receipt past its window.
With 201 owners: 37.6 and 40.0 ms with receipts due, 8.3 ms with nothing due,
plus 95–220 ms of JIT compilation that production does not pay (`jit = off`).
That is roughly 0.04–0.2 ms per owner each 10-second prune. Main's global
scan by settlement time took 0.05 ms here because nothing was 30 days old,
but cannot serve per-plan windows: with a 1-day window it would step over
every longer-window receipt between 1 and 14 days old on each pass. At
thousands of owners the walk should become a bounded page per pass.

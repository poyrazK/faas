# V12 provisional re-run — 2026-10-22

V12 on main after batched fanout claims (#5161). The owner chose to measure on
`gregale-internal-test-1` until launch hardware is available, so these results
are **provisional**: event caps stay where they are, and the binding run is
still the reference node. Method and configurations as in the
[2026-10-20 baseline](../20261020-event-substrate-bench/REPORT.md), plus an
offered-rate sweep run twice and ordered work-policy lanes.

## Environment

| | |
|---|---|
| Host | `gregale-internal-test-1`, GCP n2-highmem-2 (2 vCPU, 16 GB), Ubuntu 24.04.5, shared with other workloads |
| PostgreSQL | 16.15, a throwaway cluster on the same host, with `pg_stat_statements` (`track=all`, `track_planning=on`), reset before each run |
| Code | main at `fa611e89`, Go 1.26.10 |
| Runs | 16:43–17:12 UTC, started once the 1- and 5-minute load stayed below 1.5 for three minutes |
| Load | 1-minute load median 3.1 (standard), 3.7 (sweep 1), 4.8 (ordered), 4.1 (sweep 2), including the benchmark itself; other workloads ran alongside (`load.log`) |

## Results

End-to-end pipeline, publish to handler, by offered rate. The sweeps run on a
200-event backlog (`event-pipeline-sweep-1.json`, `event-pipeline-sweep-2.json`);
the standard run offers 50 and 200 after its 2,000-event stages
(`event-pipeline.json`):

| Offered | Sweep 1 | Sweep 2 | Standard |
|---|---|---|---|
| 5/s | 5.0/s, p95 275 ms | 5.0/s, p95 406 ms | |
| 10/s | 10.0/s, p95 72 ms | 10.0/s, p95 91 ms | |
| 20/s | 20.0/s, p95 100 ms | 20.0/s, p95 375 ms | |
| 50/s | **49.9/s, p95 232 ms** | 32.8/s, p95 7.7 s | 26.8/s, p95 17.2 s |
| 75/s | 36.6/s, p95 17.5 s | 27.0/s, p95 32.6 s | |
| 100/s | 28.5/s | 25.6/s, 40 pending | |
| 150/s | 23.5/s, 1,200 pending | 24.9/s, 1,082 pending | |
| 200/s | | | 22.1/s, 2,300 pending |

The 1-minute load while 50/s was offered was about 3–4.5 in sweep 1, 5 in
sweep 2 and 6–9 in the standard run. The
[#5161 evidence](../20261022-event-fanout-batch-claims/REPORT.md) held 50/s at
48.5/s (p95 830 ms) at load 1.8.

Isolated stages:

| Stage | Standard (2,000 events) | Sweep 1 (200) | Sweep 2 (200) |
|---|---|---|---|
| publish | 425 events/s | 160 events/s | 176 events/s |
| fanout | 72.4 events/s | 114.5 events/s | 89.9 events/s |
| drain | 88.8 invocations/s | 50.7 invocations/s | 35.7 invocations/s |
| webhook | 156 deliveries/s | | |

Ordered work-policy lanes, 20 keys per tenant (`event-pipeline-ordered.json`):
fanout 31.5 events/s over 2,000 events; the pipeline holds 20/s (p95 162 ms)
and reaches 21.1/s at 50 offered.

Database time per unit in the standard run (top-level statements, execution
plus planning; `statements.tsv`):

| Path | Per unit | Largest statements |
|---|---|---|
| Publish | 9.2 ms per event | `EventStorageAppend` 7.1 ms, `EventStorageLockAccount` 1.7 ms |
| Fanout | 7.2 ms per routed recipient | `EventDeliveryCounts` 1.0 ms, recipient claims 0.9 ms, adoption 0.6 ms per receipt |
| Invocation lifecycle | 21.4 ms per invocation | `FinancialComputeAppScopes` 5.4 ms (3.9 calls), async quota upsert 4.0 ms, dispatch claim 4.3 ms, state update 3.8 ms |

## Findings

1. **The pipeline sustains 20 events/s in every run** (p95 at most 375 ms).
   **50/s holds only when the host is otherwise quiet**: 49.9/s at load 3–4.5,
   27–33/s at load 5–9. 75/s never holds. On this host the sustained rate is
   between 20 and 50 events/s, depending on what else runs.
2. Under V12 (caps at most half the highest sustained rate), these numbers
   bound the aggregate event rate the caps admit on one control plane at
   **10 events/s** until the reference node is measured. Per-account caps sit
   far below that today: settled receipts stay charged for 30 days, which
   holds a Scale account to about 0.4 events/s sustained.
3. **Fanout no longer dominates database time** (7.2 ms per recipient, 28.0 ms
   before #5161). The larger costs are now publish (9.2 ms per event) and the
   invocation lifecycle (21.4 ms per invocation), where
   `FinancialComputeAppScopes` runs about four times per invocation. On two
   vCPUs shared with PostgreSQL, these together set the ceiling between 50 and
   75 events/s.

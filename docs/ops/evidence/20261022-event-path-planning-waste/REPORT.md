# Event path planning waste — 2026-10-22

V12 runs of `perf/event-path-planning-waste` against main, back to back on
`gregale-internal-test-1`, after the
[provisional V12 run](../20261022-v12-provisional/REPORT.md) found the event
path's database time dominated by work that changed nothing: SQL-language
functions re-planned on every call, a compute-budget scopes read for accounts
without budgets, and an outbox row written back unchanged.

## Environment

| | |
|---|---|
| Host | `gregale-internal-test-1`, GCP n2-highmem-2 (2 vCPU, 16 GB), Ubuntu 24.04.5, shared with other workloads |
| PostgreSQL | 16.15, a throwaway cluster on the same host, with `pg_stat_statements` (`track=all`, `track_planning=on`), reset before each run |
| Code | branch `0f1ef3a1` and main `9be75f82`, Go 1.26.10 |
| Runs | 20:06–20:31 UTC in the order main standard, branch standard, main sweep, branch sweep, after the load stayed below 1.5 for three minutes |
| Load | 1-minute load median 2.1 (main standard), 2.8 (branch standard), 3.4 (main sweep), 3.8 (branch sweep), including the benchmark itself (`load.log`) |

## Results

Standard run (`event-pipeline.json`, `main-event-pipeline.json`):

| | Main | Branch |
|---|---|---|
| Database time per delivered event (top-level statements, execution plus planning) | 37.8 ms (185.0 s, 4,900 events) | **26.3 ms** (134.1 s, 5,100 events) |
| publish | 488 events/s | 628 events/s |
| fanout, 2,000-event backlog | 93.4 events/s | 85.3 events/s (path unchanged) |
| drain | 106.8 invocations/s | **143.7 invocations/s** |
| pipeline at 50/s | 39.9/s, p95 1,164 ms | **48.7/s, p95 437 ms** |
| pipeline at 200/s | 24.8/s, 2,100 pending | 27.5/s, 1,900 pending |
| webhook | 158 deliveries/s | 158 deliveries/s |

Offered-rate sweep on a 200-event backlog (`event-pipeline-sweep.json`,
`main-event-pipeline-sweep.json`):

| Offered | Main | Branch |
|---|---|---|
| 20/s | 20.0/s, p95 68 ms | 20.0/s, p95 88 ms |
| 50/s | 49.9/s, p95 195 ms | 40.0/s, p95 240 ms, p99 397 ms; one event of 1,000 waited 5.1 s for the safety tick |
| 75/s | 36.2/s | 29.7/s |
| 100/s | 25.1/s, 70 pending | **32.7/s**, 0 pending |
| 150/s | 18.0/s, 1,570 pending | **30.1/s**, 700 pending |

The statements the change targets (standard run; `statements.tsv`,
`main-statements.tsv`):

| Statement | Main calls | Main execution + planning | Branch calls | Branch execution + planning |
|---|---|---|---|---|
| `FinancialComputeAppScopes` | 18,987 | 20.1 + 0.3 s | 0 | |
| `FinancialBudgetList` | 18,987 | 0.4 + 0.0 s | 0 | |
| `FinancialComputeAppPolicies` (new) | 0 | | 19,906 | 0.3 + 0.0 s |
| `financial_compute_deployment_environment` body | 18,987 | 1.2 + 13.8 s | 0 | |
| `app_workflow_definitions` body | 7,000 | 0.3 + 3.6 s | 0 | |
| workflow recipients in `enqueue_event_fanout` | 7,000 | 6.9 + 1.1 s | 7,000 | 0.8 + 0.9 s |
| `invocation_async_subject` body | 9,804 | 0.5 + 4.0 s | 10,200 | 0.6 + 0.0 s |
| snapshot write-back | 7,000 | 4.8 s | 0 | |
| `workflow_event_code_refs` delete | 14,000 | 0.6 s | 7,000 | 0.2 s |
| `EventStorageAppend` (publish, includes the triggers above) | 7,000 | 34.4 s | 7,000 | 17.6 s |

## Findings

1. **Database time per delivered event fell 30%**, from 37.8 to 26.3 ms.
   The budget check went from about 4.2 ms per invocation to under 0.1 ms,
   publish (`EventStorageAppend`) from 4.9 to 2.5 ms per event, and the drain
   ran 35% faster.
2. **The pipeline holds more under overload**: 32.7/s at 100 offered and
   30.1/s at 150, against 25.1/s and 18.0/s on main. In the standard run it
   held 50/s with p95 437 ms, where main fell to 39.9/s.
3. At 50/s in the sweep the branch delivered 999 of 1,000 events with p99
   397 ms; the last one waited for the fanout's 5 s safety tick, so the
   stage's rate reads 40/s. Main's standard run shows the same single
   straggler (max 5,117 ms): an `event_published` wake is occasionally
   missed. That tail is not part of this change.
4. 75/s still does not hold on this host. Fanout (6.2 ms per routed
   recipient, unchanged) and the invocation lifecycle's remaining statements
   are the next costs.

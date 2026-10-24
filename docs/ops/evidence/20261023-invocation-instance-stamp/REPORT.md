# Asynchronous instance stamp — 2026-10-23

`perf/invocation-instance-stamp` commits the drain's post-wake instance stamp
without waiting for its WAL flush (`stamp_invocation_instance`), and re-keys
the dispatching index on `due_at` so no index holds `instance_id`.

## Environment

| | |
|---|---|
| Host | `gregale-internal-test-1`, GCP n2-highmem-2 (2 vCPU, 16 GB), PostgreSQL 16.15 in a throwaway cluster |
| Code | branch `4c9a6347` and main `f46e54a7`, Go 1.26.10 |
| Runs | 14:33–14:58 UTC: main standard, branch standard, main sweep, branch sweep |
| Load | 1-minute load median 1.7 (main standard), 3.0 (branch standard), 2.4 (main sweep), 1.7 (branch sweep) |

## Results

Offered-rate sweep on a 200-event backlog (`event-pipeline-sweep.json`,
`main-event-pipeline-sweep.json`):

| Offered | Main | Branch |
|---|---|---|
| 20/s | 20.0/s, p95 38 ms | 20.0/s, p95 35 ms |
| 50/s | 49.9/s, p95 173 ms | 49.9/s, p95 74 ms |
| 75/s | 29.9/s, p95 26.7 s | **48.3/s**, p95 11.0 s |
| 100/s | 23.8/s, 166 pending | **33.1/s**, 0 pending |
| 150/s | 25.4/s, 1,065 pending | 29.4/s, 756 pending |

Standard run (`event-pipeline.json`, `main-event-pipeline.json`): the
pipeline held 50/s in both, with p95 159 ms on the branch against 409 ms on
main. The branch run had the busier host, and its fanout stage, which the
change does not touch, ran at half main's rate.

The stamp itself (`statements*.tsv`): 0.72–0.77 ms per call on the branch
against 0.83–0.84 ms on main. `pg_stat_statements` does not count the
commit's WAL flush, which is what the asynchronous commit removes; most of
what remains is the update's triggers, which fire on every update of
`invocations` whichever column changes.

## Findings

1. The sweep held more under overload: 48 events/s at 75 offered against 30,
   and every event at 100 offered. Each invocation's lifecycle now waits for
   one WAL flush fewer.
2. Index maintenance was a small part of the stamp's cost; the triggers that
   fire on every `invocations` update are most of the rest.

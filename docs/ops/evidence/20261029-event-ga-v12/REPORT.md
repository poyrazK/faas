# V12 against the event GA target, provisional (2026-10-29)

The first V12 measured against the GA gate's 50 events/s target
([ADR 20261029093000](../../../adr/20261029093000-event-ga-gate.md)).

- **Host:** `gregale-internal-test-1` (2 vCPU, PostgreSQL 16.15, shared with
  other jobs). Measurement started after a 79-minute wait for a quiet host.
- **Build:** `5fa05503`, the GA-gate commit on top of #5836 (backlog
  throughput). Main did not build at the time: `pkg/imaged` declared
  `scannerIdentity` twice after #5776.
- **Status:** provisional. Promotion needs a run on the OVH reference host.

## Result: the target is not met on this host

| Run | 1-min load at start | 20/s | 35/s | **50/s** | 65/s | 80/s |
|---|---:|---|---|---|---|---|
| `sweep-2` | 4.85 | 100 %, p95 0.18 s | 100 %, p95 0.05 s | **99.8 %, p95 0.34 s ✅** | 79 % | 44 % |
| `sweep-1` | 5.53 | 100 %, p95 0.16 s | 99.6 %, p95 0.16 s | **89 %, p95 2.1 s ❌** | 56 % | 49 % |
| `standard` (2,000-event stages first) | 1.60 | — | — | **49 %, p95 17.8 s ❌** | — | — |
| `ordered` (20 keys; target 20/s) | 2.98 | **100 %, p95 0.34 s ✅** | — | 45 % | — | — |

A rate passes when ≥ 95 % of the offered rate is achieved, nothing is pending
after the 60 s settle window, and p95 publish-to-handler is ≤ 5 s.

- **50 events/s:** sustained in one sweep of three. 35/s held in both sweeps.
  The ordered target of 20/s is met.
- **Stage throughput** (`standard` / `sweep`):

  | Stage | `standard` | `sweep` |
  |---|---:|---:|
  | Publish | 425/s | 141–206/s |
  | Fanout over the backlog | 39/s (2,000-event backlog) | 85–86/s (200-event backlog) |
  | Drain | 96/s | 34–35/s |
  | Webhook delivery | 122/s | — |

## Where the time goes

`*-statements.tsv` holds `pg_stat_statements` for each run. Per published
event, the event path costs about 25 ms of database time:

| Statement | Per call | Note |
|---|---:|---|
| `EventStorageAppend` (the `events` insert and its triggers) | 5.0–6.3 ms | every publish |
| invocation claim (`with target as materialized …`) | 2.8–5.7 ms | every delivery |
| `EventDeliveryCounts` | 4.8 ms, plus 2.4 s of planning in total | `standard` only: its cost grows with the account's outstanding deliveries, so after the 2,000-event stages each routed recipient pays for the backlog |
| outbox insert trigger | 2.3–3.0 ms | every publish |
| `refresh_event_routing_backlog` and the backlog projection | about 3 ms per event | |
| `EventStorageLockAccount` | 1.4–1.6 ms | every publish |

At about 25 ms of database time per event, on 2 vCPUs that also run the Go
benchmark, the pipeline tops out near 40–50 events/s. It drops below that
once host load or a larger backlog adds work.

## Findings

1. **50 events/s is not yet sustained on the 2 vCPU test node.** Under the
   gate this is a finding to fix, not a reason to lower the target. The OVH
   reference host has about 12 times the CPU, and its run decides
   promotion.
2. **`EventDeliveryCounts` grows with the backlog.** Each routed recipient
   counts the account's, app's and consumer's live deliveries, so a backlog
   of N makes routing O(N) per recipient. This is what separates `standard`
   (49 %) from the sweeps.
3. **The publish path costs about 9 ms of database time per event**
   (`EventStorageAppend`, the outbox trigger and the account lock), and
   delivery costs another 5–10 ms. These are the remaining levers for
   headroom on small hosts.

## Files

- `<run>-event-pipeline.json`, `standard-webhook-delivery.json`: the
  benchmark's reports.
- `<run>-statements.tsv`: top statements by total time during the run.
- `<run>-load-start.txt`, `<run>-window.txt`: host load and the run's UTC
  window.
- `commit.txt`: the commit measured.

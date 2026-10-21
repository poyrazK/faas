# Event fanout claim fix — 2026-10-21

V12 re-run after `event_recipient_order_blocker` started returning before its
backlog scan for recipients without a work policy (migration
`20261020194841377`). Same host, method and configurations as the
[2026-10-20 baseline](../20261020-event-substrate-bench/REPORT.md).

## Environment

| | |
|---|---|
| Host | `gregale-internal-test-1`, GCP n2-highmem-2 (2 vCPU, 16 GB), Ubuntu 24.04.5 |
| PostgreSQL | 16.15, a throwaway cluster on the same host, separate from the host's main cluster |
| Code | `fix/event-fanout-claim-order-scan` at `0bed12a8`, Go 1.26.9 |
| Load | Shared host, busier than the baseline: 1-minute load median 10.3 (min 6.7, max 24.0) on 2 vCPUs, against 7.8 for the baseline |

## Results

End-to-end pipeline, publish to handler (`event-pipeline-low-rates.json` and
`event-pipeline.json`):

| Offered | Baseline achieved | Baseline p95 | Fixed achieved | Fixed p95 | Fixed pending |
|---|---|---|---|---|---|
| 5/s | 5.0/s | 1,249 ms | 5.0/s | 217 ms | 0 |
| 10/s | 7.6/s | 6,739 ms | **10.0/s** | **260 ms** | 0 |
| 20/s | 9.2/s | 23,668 ms | **20.0/s** | **198 ms** | 0 |
| 50/s | 6.2/s, 502 pending | 67,678 ms | 19.0/s | 32,449 ms | 0 after settle |
| 200/s | 2.3/s, 3,750 pending | 103,555 ms | 8.8/s | 75,657 ms | 3,299 |

Isolated stages:

| Stage | Baseline | Fixed |
|---|---|---|
| fanout, 200-event backlog | 10.5 events/s, sweeps p50 8.8 s | 33.7 events/s, sweeps p50 2.9 s |
| fanout, 2,000-event backlog | 14.8 events/s, sweeps p50 5.0 s | 20.3 events/s, sweeps p50 4.6 s |
| drain | 48 invocations/s | 86 invocations/s, every invocation on its first attempt |
| publish | 307 events/s | 146 events/s (busier host) |

## Findings

1. **The sustained end-to-end rate rose from 5 to at least 20 events/s**,
   with p95 publish-to-handler 198 ms at 20/s, on a busier host. Under V12
   the event caps may now sit at up to 10 events/s on this host; the binding
   rate lies between 20 and 50 events/s.
2. **Fanout is still the first stage to fall behind**, and isolated sweeps
   still slow down as the backlog grows (20 events/s at 2,000 against 34 at
   200). The ordering scan remains for recipients with a work policy, and
   each sweep still admits recipients one at a time. Batching admission
   within a sweep, and narrowing the ordered-lane scan to its lane, are the
   next fanout steps.
3. The webhook stage is measured with its own fix
   (`fix/webhook-dispatcher-backlog-cycles`).

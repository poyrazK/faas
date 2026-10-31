# Event fanout batch claims — 2026-10-22

V12 runs of `perf/event-fanout-batch-claim` against main, on the host and
configurations of the [2026-10-20 baseline](../20261020-event-substrate-bench/REPORT.md).
The branch claims a sweep's receipts for adoption, and up to 20 recipients,
in one scan each, and makes `refresh_event_routing_backlog` PL/pgSQL.

## Environment

| | |
|---|---|
| Host | `gregale-internal-test-1`, GCP n2-highmem-2 (2 vCPU, 16 GB), Ubuntu 24.04.5 |
| PostgreSQL | 16.15, a throwaway cluster on the same host, with `pg_stat_statements` (`track=all`, `track_planning=on`), reset before each run |
| Code | branch `efb8d366` (main merged at `6865fc4f0b`) and main `005e7228`, Go 1.26.9 |
| Load | Standard and low-rate runs back to back, 14:15–14:37 UTC: 1-minute load median 1.8 for both the branch and main |

The ordered-lane runs are from earlier sessions, so their loads differ: main
`4e2a454a01` plus the benchmark's ordered mode at 00:27 UTC (load median 5.8),
and the branch at `2ff34451` plus the ordered mode at 09:44 UTC (load median
4.4).

## Results

End-to-end pipeline, publish to handler (`event-pipeline.json`,
`event-pipeline-low-rates.json`, and the `main-` files):

| Offered | Main achieved | Main p95 | Branch achieved | Branch p95 | Branch pending |
|---|---|---|---|---|---|
| 5/s | 5.0/s | 97 ms | 5.0/s | 106 ms | 0 |
| 10/s | 10.0/s | 180 ms | 10.0/s | 35 ms | 0 |
| 20/s | 20.0/s | 86 ms | 20.0/s | 43 ms | 0 |
| 50/s | 26.9/s | 16,909 ms | **48.5/s** | **830 ms** | 0 |
| 200/s | 12.8/s, 3,000 pending | 72,345 ms | 25.2/s, 2,072 pending | 66,148 ms | 2,072 |

Isolated stages:

| Stage | Main | Branch |
|---|---|---|
| fanout, 2,000-event backlog | 29.9 events/s, sweeps p50 3.0 s | **74.2 events/s**, sweeps p50 1.0 s |
| fanout, 200-event backlog | 48.1 events/s, sweeps p50 2.0 s | **112.1 events/s**, sweeps p50 0.9 s |
| drain | 111 invocations/s | 114 invocations/s |
| publish | 392 events/s | 519 events/s |
| webhook | 100 deliveries/s | 158 deliveries/s (path unchanged: run-to-run variance) |

Ordered work-policy lanes, 20 keys per tenant (`FAAS_EVENT_SUBSTRATE_BENCH_ORDERED_KEYS=20`,
offered 20 and 50 events/s; `event-pipeline-ordered.json` and
`main-event-pipeline-ordered.json`):

| Stage | Main | Branch |
|---|---|---|
| fanout, 2,000-event backlog | 0.95 events/s: 600 of 2,000 within the 10-minute stage timeout, sweeps p50 90 s | **21.7 events/s**, all 2,000, sweeps p50 4.2 s |
| pipeline at 20/s | 0.4/s, 370 pending, p95 71.8 s | **20.0/s, 0 pending, p95 98 ms** |
| pipeline at 50/s | 0.03/s, 998 pending | 24.5/s, 0 pending after settle, p95 19.9 s |

Database time of the fanout path in the standard runs (top-level `Event*`
statements except `EventStorage*`, plus `EnqueueInvocationRow`; execution
plus planning; `statements.tsv` and `main-statements.tsv`):

| | Main | Branch |
|---|---|---|
| Per routed recipient | 28.0 ms (4,000 recipients) | **6.75 ms** (4,928 recipients) |
| Receipt adoption | `EventRoutingClaimReceipt`: 4,008 calls, 9.47 ms per receipt | `EventRoutingClaimReceiptsForAdoption`: 208 calls for 5,028 receipts, 0.39 ms per receipt |
| Recipient claim | `EventRecipientClaim`: 4,007 calls, 4.50 ms per recipient | `EventRecipientClaimBatch`: 309 calls for 4,928 recipients, 0.54 ms per recipient |
| `refresh_event_routing_backlog(outbox)` (trigger) | 1.81 ms per call | 0.40 ms per call |
| `refresh_event_routing_backlog(outbox, subscription)` (trigger) | 1.08 ms per call | 0.21 ms per call |
| `EventDeliveryCounts` (query unchanged) | 5.55 ms per recipient | 1.70 ms per recipient |

## Findings

1. **Fanout is 2.3–2.5× faster, and the pipeline nearly holds 50 events/s**:
   48.5/s with p95 830 ms, against 26.9/s and p95 16.9 s on main. The binding
   rate on this host now lies between 50 and 200 events/s.
2. **Fanout database time fell from 28.0 to 6.75 ms per routed recipient.**
   Adoption and claims no longer evaluate eligibility for every due row to
   take one, and the backlog projection's trigger function no longer re-plans
   its body on every call.
3. **Ordered lanes went from unusable to 21.7 events/s.** On main every claim
   evaluated `event_recipient_order_blocked` for every due recipient. The
   batch claim evaluates it only for the ranks it can claim. The blocker
   remains the largest ordered cost (`statements-ordered.tsv`: 17,972 calls
   for 3,400 recipients, 3.3 ms each, because each call still reads the
   account's unadopted receipts), so ordered fanout runs at about a third of
   the unordered rate. A lane index covering adopted receipts was measured on
   top of this branch and did not help, so it is not part of this change.
4. `EventDeliveryCounts` is unchanged here. Its cost follows the plan the
   statistics lead to (5.5 ms per call on main, 1.7 ms here, the same query),
   and it is the largest remaining statement of the unordered run.

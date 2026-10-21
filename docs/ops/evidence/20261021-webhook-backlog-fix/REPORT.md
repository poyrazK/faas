# Webhook backlog fix — 2026-10-21

V12 webhook stage re-run after the dispatcher started claiming again as soon
as a delivery finishes while a backlog remains, instead of once per 5 s tick.
Same host and method as the
[2026-10-20 baseline](../20261020-event-substrate-bench/REPORT.md):
`gregale-internal-test-1` (2 vCPU, shared and busy), a throwaway PostgreSQL
16.15 cluster, branch `fix/webhook-dispatcher-backlog-cycles` at `9c1dfa0a`.

| | Baseline | Fixed |
|---|---|---|
| 1,000 deliveries, 64 subscribers | 160 s | 6.6 s |
| Rate | 6.2 deliveries/s | **152 deliveries/s** |
| Enqueue to receiver p50 / p95 / p99 | 80.4 / 150.1 / 155.1 s | 6.3 / 6.7 / 6.8 s |

Production tick (5 s), claim cap (32) and in-flight limit (64), and a local
receiver that answers at once. The whole backlog was enqueued before the
dispatcher started, so the fixed latency is mostly the wait for the first
tick; after it the backlog drained in about 1.5 s. Real receivers are slower,
so the in-flight limit and each subscription's 4-delivery cap now bound
throughput instead of the tick.

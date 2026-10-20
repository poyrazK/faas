# Event substrate benchmark — 2026-10-20

First run of spec Appendix D V12 (`make bench-event-substrate`,
[method](../../event-substrate-benchmark.md)).

## Environment

| | |
|---|---|
| Host | `gregale-internal-test-1`, GCP n2-highmem-2 (2 vCPU, 16 GB, pd-ssd), Ubuntu 24.04.5 |
| PostgreSQL | 16.15, a throwaway cluster on the same host (default settings, `max_connections=200`, Unix socket only), separate from the host's main cluster |
| Code | `feat/event-substrate-benchmark` at `4d75ab6f`, Go 1.26.9 |
| Load | Shared host: other users' jobs ran throughout. One-minute load average during run 1: median 7.8, min 3.9, max 21.3 on 2 vCPUs |

This is not the reference node and the host was busy, so the absolute
numbers are pessimistic. The fanout scaling behaviour below follows from the
query plan, not from host load.

## Results

Run 1, defaults (`event-pipeline.json`, `webhook-delivery.json`):

| Stage | Items | Rate | Latency (ms) p50 / p95 / p99 | Notes |
|---|---|---|---|---|
| publish | 2,000 | 307 events/s | 31 / 78 / 148 | 12 publishers |
| fanout | 2,000 | **14.8 events/s** | 5,014 / 13,903 / 16,655 per sweep | 2,000-event backlog |
| drain | 2,000 | 48 invocations/s | — | every invocation on its first attempt, no warnings |
| pipeline@50/s | 498 of 1,000 | 6.2 events/s | 33,369 / 67,678 / 69,830 | 502 still pending after the 60 s settle window |
| pipeline@200/s | 250 of 4,000 | 2.3 events/s | 78,584 / 103,555 / 104,391 | 3,750 pending |
| webhook | 1,000 | 6.2 deliveries/s | 80,446 / 150,122 / 155,083 | production tick 5 s, cap 32, 64 subscribers |

Run 2, a 200-event backlog and offered rates of 5, 10 and 20 events/s
(`event-pipeline-low-rates.json`):

| Stage | Items | Rate | Latency (ms) p50 / p95 / p99 |
|---|---|---|---|
| fanout | 200 | 10.5 events/s | 8,792 / 10,308 / 10,308 per sweep |
| drain | 200 | 18 invocations/s | — |
| pipeline@5/s | 100 | **5.0 events/s** | 509 / 1,249 / 1,360 |
| pipeline@10/s | 200 | 7.6 events/s | 970 / 6,739 / 7,344 |
| pipeline@20/s | 400 | 9.2 events/s | 13,902 / 23,668 / 24,085 |

The pipeline warnings were shutdown noise (housekeeping queries cancelled
when a stage stopped, and the drain reclaiming the previous stage's leases).
No dispatch was retried. The fake VMM recorded no cold boots.

## Findings

1. **Fanout routing is the binding constraint, and it degrades with the
   backlog.** `EventRecipientClaim`
   (`pkg/state/event_recipient_queries.sql:63-81`) evaluates
   `event_recipient_order_blocked` for every candidate recipient before
   `ORDER BY … LIMIT 1`. That function calls `event_recipient_order_blocker`
   (migration `20261008210546679`), which joins every prior outbox row of the
   account and expands each recipient snapshot, even for subscriptions with
   no ordering. A sweep admits at most 100 recipients, one claim at a time,
   so routing a backlog costs O(backlog²): sweeps took 5–14 s over a
   2,000-event backlog.
2. **The pipeline sustains about 5 events/s end to end on this host.** At
   5 events/s every event reached its handler with p95 1.2 s. At 10 events/s
   and above the backlog grew while load was offered, and once routing fell
   behind, latency grew with the backlog: p50 33 s at 50 events/s, with half
   the events still pending a minute after publishing stopped.
3. **The drain and publish are not the bottleneck at these rates.** The drain
   ran 48 invocations/s at production concurrency (8 workers), and publish
   ran 307 events/s. The 6.7 invocations/s seen in a laptop smoke run came
   from that run's setup (no planner statistics, instances seeded after the
   engine), which this harness now fixes.
4. **Webhook delivery sits at its design ceiling.** 6.2 deliveries/s matches
   one 32-row claim per 5 s tick; a 1,000-delivery backlog took 160 s, with
   p50 enqueue-to-receiver 80 s.

## V12 verdict

V12 caps sit at ≤ 50 % of the highest offered rate the pipeline sustains.
Here that rate is 5 events/s, so ≤ 2.5 events/s (about 216,000 events/day)
for the whole platform on this host. One Scale account's current 30-day
event budget averages about 0.4 events/s, so six Scale accounts publishing
at their full budget would saturate this host. **The event caps must not
rise until fanout routing is fixed**; fix `EventRecipientClaim` so that
unordered subscriptions skip the ordering scan and ordered lanes are not
re-scanned per candidate, then re-run this benchmark. The cap values
themselves should come from a run on the reference node.

# Event substrate benchmark

`make bench-event-substrate` measures the control-plane half of event-driven
work: how fast events are accepted, routed to subscribers, dispatched to
handlers and delivered to webhook receivers. It is spec Appendix D **V12**.
Its numbers set the ceiling for the event storage, queue depth and
workflow caps in `pkg/api/limits.go`; raise those caps only with evidence
from this benchmark on the reference node.

## What runs

The benchmark drives the production code paths against PostgreSQL. It does
not boot VMs: a stand-in for gatewayd-internal records when each invocation
reaches its handler and answers 200, and every app has a running instance,
so the drain takes the warm wake path. A cold boot during the run fails it.

| Stage | Production path | Reported |
|---|---|---|
| `publish` | `appevents.Service.PublishEvent`: validation, ADR-615 storage admission, the outbox trigger and the receipt, through an apid-sized pool (12 connections). HTTP auth middleware is excluded. | events/s, per-request latency |
| `fanout` | `Loop.runEventFanoutSweep` run back to back over the published backlog: recipient routing and invocation admission (ADR-647) | events/s, per-sweep latency |
| `drain` | `sched.Drain` with production dispatch concurrency over the routed backlog: claim, warm wake, dispatch, completion | invocations/s |
| `pipeline@N/s` | N events/s offered for 20 s while fanout runs exactly as schedd schedules it (an `event_published` wake into one coalescing slot plus the 5 s safety tick) and the drain runs beside it | sustained events/s, publish-to-handler latency, events still pending after a 60 s settle window |
| `webhook` | `webhook.Dispatcher` with the production tick, claim cap and in-flight limit, delivering to a local receiver | deliveries/s, enqueue-to-receiver latency |

Every stage also reports the warnings the scheduler code logged while it
ran, with up to three samples; the drain stage adds a count of deliveries by
dispatch attempts and their most common last errors. A rate measured with
warnings or retries is not a capacity number: find the cause first. The run
seeds every tenant before building the scheduler (so its admission ledger
sees their running instances) and runs `ANALYZE` after seeding and after
publishing, because production tables carry planner statistics a fresh
database lacks.

A stage that cannot finish its backlog within 10 minutes stops there and
reports what it processed and what is still pending, instead of failing the
run.

The isolated stages show each stage's ceiling on the host. The pipeline
stages show what a customer sees. V12 counts an offered rate as sustained
when the achieved rate is at least 95 % of it, nothing is pending after the
settle window, and p95 publish-to-handler is at most 5 s. A rate the
pipeline cannot sustain shows a lower achieved rate, latency that grows with
the backlog, or events still pending. `pending=0` alone is not enough: a
backlog can clear during the settle window after the offered load stops.

## Running it

Point `DATABASE_URL` at a disposable PostgreSQL server whose role may
create databases. The benchmark migrates a template database once and runs
each stage set in its own clone (`FAAS_PGTEST_TEMPLATE_DATABASE=1`, as CI
does), so other work on the server does not share its tables. Never point it
at a production server. Migrations take a few minutes before the first stage
starts.

```bash
DATABASE_URL='postgres:///postgres?host=/run/postgresql' \
FAAS_EVENT_SUBSTRATE_BENCH_DIR=docs/ops/evidence/$(date -u +%Y%m%d)-event-substrate-bench \
  make bench-event-substrate
```

The target prints one `event-substrate:` line per stage and, when
`FAAS_EVENT_SUBSTRATE_BENCH_DIR` is set, writes `event-pipeline.json` and
`webhook-delivery.json` there. Each file records the host, PostgreSQL
version and configuration next to the results.

| Variable | Default | Meaning |
|---|---|---|
| `FAAS_EVENT_SUBSTRATE_BENCH_EVENTS` | 2000 | events for the isolated stages |
| `FAAS_EVENT_SUBSTRATE_BENCH_ACCOUNTS` | 10 | Scale accounts, one app and subscription each |
| `FAAS_EVENT_SUBSTRATE_BENCH_PUBLISHERS` | 12 | concurrent publishers (apid's pool size) |
| `FAAS_EVENT_SUBSTRATE_BENCH_DRAIN_CONCURRENCY` | production default | drain dispatch workers |
| `FAAS_EVENT_SUBSTRATE_BENCH_HANDLER_MS` | 0 | simulated handler time per invocation |
| `FAAS_EVENT_SUBSTRATE_BENCH_PIPELINE_RATES` | `50,200` | offered end-to-end rates, events/s |
| `FAAS_EVENT_SUBSTRATE_BENCH_PIPELINE_SECONDS` | 20 | seconds of offered load per rate |
| `FAAS_EVENT_SUBSTRATE_BENCH_WEBHOOK_DELIVERIES` | 1000 | webhook deliveries |
| `FAAS_EVENT_SUBSTRATE_BENCH_WEBHOOK_SUBSCRIBERS` | 64 | webhook endpoints (each has a 4-delivery in-flight cap) |
| `FAAS_EVENT_SUBSTRATE_BENCH_RECEIVER_MS` | 0 | simulated receiver time |
| `FAAS_EVENT_SUBSTRATE_BENCH_WEBHOOK_TICK_MS`, `_CAP`, `_MAX_IN_FLIGHT` | production defaults | dispatcher tuning experiments |

Keep the defaults for evidence. Change one variable at a time to explore a
bottleneck, and say so in the evidence report.

## Recording evidence

Commit the JSON files and a short `REPORT.md` under
`docs/ops/evidence/<YYYYMMDD>-event-substrate-bench/`, then link the report
from the V12 row and `docs/STATUS.md`. Name the host. Laptop runs are useful
for comparing branches; only reference-node runs set caps.

# ADR 20261103113952 · Request analytics from per-minute rollups

- **Status:** accepted.
- **Date:** 2026-11-03
- **Amends:** ADR-127 (production debugger request telemetry), ADR
  20261026143210 (per-plan telemetry ingest caps)
- **Related:** ADR 20261027093220 (daily billing evidence)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Customer request analytics (`GET /v1/apps/{slug}/analytics`, the timeseries,
the dashboard panel, `gregale metric requests`, the MCP analytics tools) read
`request_telemetry`. That table is ADR-127's capped, per-request debugger
store. Three properties make it the wrong source for traffic totals:

1. **Ingest caps cut the totals.** apid keeps at most
   `DebugTelemetryRequestsPerMinute` rows per account (Hobby 1,000, Pro
   10,000, Scale 50,000). The gateway's collapse key has 10 ms latency
   buckets, guest measurements, country, UA family, referrer, consumer, wake
   and node in it. In a measurement it stored 77–100% of requests as separate
   rows. So analytics undercounts above about 20 / 190 / 1,000 requests per
   second per account. The rows over the cap are dropped as `rate_limited`
   and are absent from every analytics number.
2. **Every read scans raw rows.** A 7-day window on a busy Pro app reads
   millions of rows within the 10 s analytics statement budget.
3. **Retention of raw rows is the debugger's retention.** It is the expensive
   store, and analytics inherits its cost.

## Decision

apid aggregates every request telemetry row it receives for a plan that keeps
telemetry into per-minute rollups, before the ingest cap is applied. The
rollups go to a Postgres table, and analytics reads use them for every window
they cover. Raw `request_telemetry` rows stay the debugger's evidence and keep
their cap.

### What a rollup row holds

`request_analytics_minutes` has one row per app, UTC minute, deployment,
dimension and HTTP status:

- `dimension_kind` is `route`, `country`, `referrer_host`, `ua_family` or
  `consumer_id`. A `route` row also carries the method. Other kinds carry an
  empty method.
- `dimension` is the value. For each app, minute and kind other than
  `route`, the aggregator keeps at most `RequestAnalyticsRollupDimensionsMax`
  distinct values per flush, and folds the rest into `__other__`. Routes
  are route templates, which the app's route table already bounds, so they
  are not folded.
- The measures are `requests`, `error_requests` (status ≥ 400),
  `cold_boots`, and `latency_hist`. `latency_hist` is a fixed histogram over
  `api.RequestAnalyticsLatencyBoundsMS`.

A percentile from the histogram is the upper bound of the bucket that holds
the rank. That is the same rule the raw queries use over the gateway's
latency representatives, with wider buckets: 10 ms wide up to 100 ms, and at
most 25% above the true value from 100 ms to 10 s. The overflow bucket above
60 s reports the minute's maximum latency. Analytics already documents its latencies as bucketed.

Guest execution, CPU and RSS, dependency spans, wake boot and per-deployment
cost stay on raw rows. They are sampled evidence, and the responses already
say so.

### Where rows are counted

The receiver adds the row to an in-memory aggregator after the plan lookup.
This is the plan's `DebugTelemetryEnabled` gate, so a Free app's rows are
still `not_retained` and never rolled up. The ingest cap is applied after the
aggregator. So `inserted` and `rate_limited` rows are both counted.

The aggregator flushes every `RequestAnalyticsRollupFlushInterval`
(10 s) and on shutdown. Each flush is a single additive
`INSERT … ON CONFLICT DO UPDATE` statement in chunks. Several apid replicas
add to the same rows safely.

A gateway replays a batch when it misses the response. The aggregator keeps
the event IDs of recent rows (`RequestAnalyticsRollupSeenEventsMax`; replays
arrive within seconds) and counts a replayed ID only once. A replay that
reaches a different apid replica, or one that arrives after an apid restart,
can count a row twice. A crash loses the unflushed 10 s. Both are bounded and
far smaller than today's loss above the cap. The design accepts them instead
of a durable per-row ledger, which would reintroduce the per-row write this
change removes.

### Reads

The analytics summary, its groups (route, status, country, referrer host, UA
family, consumer) and the hourly timeseries, grouped or not, read the
rollups in one query per answer (`RequestAnalyticsRollupGroups`). It sums the
measures and merges the histograms (`request_analytics_hist_sum`) per group,
keeps the top groups and folds the rest into `__other__`, as the raw queries
do. apid turns the merged histograms into percentiles. A minute counts when
it starts inside the window, so a window edge that is not on a minute moves
by less than a minute.

The sampled measures (cold request and wake boot p95, guest execution, CPU,
RSS) still come from raw rows. They are read for the same groups within
`RequestAnalyticsSampledMeasuresTimeout` (3 s). Past that they are left out,
and the counts and latencies are still answered. Dependency spans, the
per-deployment cost split and the route deployment observations stay on raw
rows. A grouped timeseries that filters by route but groups by a dimension
other than route or status also stays on raw rows, because only route rows
carry a route.

### Coverage

`request_analytics_rollup_coverage` stores, per app, the first minute the
rollups hold. Reads use the rollups only for windows that start at or after
that minute plus `RequestAnalyticsSettleLag`. Older windows fall back to raw
rows. The coverage minute never moves, and the row goes with the app. During a
rolling upgrade an old apid replica does not write rollups, so coverage starts
only after the first flush, and the settle lag covers the upgrade.

### Retention

meterd's request telemetry retention loop also sweeps rollups. It uses the
same plan ladder as raw rows (Hobby 3 d, Pro 7 d, Scale 14 d, Free 0, unknown
or deleted 1 d), so analytics windows and their clamp are unchanged. The
table is not partitioned. At one row per route, status and minute it is one
to three orders of magnitude smaller than the raw table, so a bounded batch
`DELETE` by `bucket_start` is enough. The sweep matches on `ctid`, which is
unique only because the table is not partitioned. If it is ever partitioned,
match `(tableoid, ctid)`, as the raw sweep does.

## Consequences

- Analytics totals stop depending on the ingest cap. The gateway's own losses
  (ring overwrite, exhausted retries) are still possible and still reported
  through `telemetry_complete` and `telemetry_gaps`.
- apid writes one rollup statement per flush, not one per request.
- Rollup percentiles are coarser than raw percentiles. The analytics guidance
  text says latencies are bucketed.
- A long window on a busy app no longer scans its raw rows for counts and
  latencies, so it stops failing on the analytics statement budget. The
  response shape is unchanged.

## Alternatives considered

- **Raise the ingest caps.** This keeps one Postgres row and one log event per
  request, which is the cost the caps exist to bound.
- **Aggregate in the gateway.** The minute collapse already exists behind a
  flag, but it keeps every debugger dimension in its key. Gateways also have
  no view across nodes, and the plan gate lives in apid.
- **Upsert rollups per row in the insert transaction.** Exact under replay,
  but five upserts per request is more write load than the raw insert.

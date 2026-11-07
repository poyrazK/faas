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

## Amendment 1 (2026-11-04): hardening after the first review

A review of the shipped rollups found two faults in them and one in the
telemetry path they read from:

1. **One client could stop an apid's rollups.** The aggregator cut text
   dimensions at 512 bytes, through a multi-byte rune when a value was
   longer. A `Referer` host of 253 three-byte runes passes the gateway's
   normalization, so any client could send one. Postgres refused the whole
   flush (`invalid byte sequence for encoding "UTF8"`), the aggregator kept
   the rows for retry, and every later flush on that apid failed, for every
   app. Coverage was already recorded, so analytics under-reported silently.
   Now text is cut on a rune boundary (invalid UTF-8 and NUL removed), a
   flush that Postgres refuses for its data (SQLSTATE class 22 or 23) is split
   until the refused rows stand alone and those are dropped, and only other
   errors keep the rows for retry.
2. **Lost rollups were invisible.** apid now records each app minute whose
   rollups it lost (refused rows, or a full buffer) in
   `request_analytics_rollup_gaps`. Analytics answered from rollups report a
   window holding such a minute as incomplete with `telemetry_window_incomplete`.
   meterd sweeps markers after 15 days.
3. **The route deployment split mixed stores.** Each route's total came from
   the rollups but its per-deployment counts from the capped raw rows, so
   past the cap "other deployments" absorbed the missing requests and their
   compute cost. Both deployment splits now take their counts from the
   rollups (`RequestAnalyticsRollupRouteDeployments`) when rollups answer;
   commit, tag and CPU still come from raw rows.
4. **The busiest values were not the ones kept.** A flush kept the first 50
   values per app minute and kind, so a busy consumer that arrived late went
   to `__other__`. The buffer now holds up to 200 values and the flush keeps
   the 50 with the most requests.

The gateway publisher drained one 256-row batch per tick. Its wake hook
re-arms only when a drain leaves the recorder under half full, so once rows
arrived faster than one batch could ship, delivery fell to the 5 s ticker,
256 rows per 5 s. At 1.25 times ship capacity 6% of rows were delivered. A
tick now keeps draining while a full batch waits, bounded by one pass over
the ring.

## Amendment 2 (2026-11-05): an hourly tier, and sampled detail is best-effort

A review of long windows found three more problems.

1. **Long windows read too slowly.** A 7-day window of a busy app took 17 to
   28 s on rollups. Reads summed every minute row (10,080 buckets per group)
   through a PL/pgSQL histogram aggregate, inside a materialized CTE that
   also held the histograms for the top-groups pass. Three changes:
   - apid also adds every flush to `request_analytics_hours`, in the same
     transaction as `request_analytics_minutes`. It has the same columns and
     key at hour grain. Reads take the whole hours inside the window from it
     and the partial edge hours from minutes, so 14 days read 336 buckets per
     group instead of 20,160, and the answer is the same as minutes alone
     give.
   - `request_analytics_rollup_coverage.hours_covered_since` is the first
     minute the hourly tier holds. The tier is read only from the hour after
     that minute plus `RequestAnalyticsSettleLag`, so an app whose minutes
     predate the tier reads minutes until whole hours exist. A flush sets it
     the way it sets `covered_since`, so the first flush after the upgrade
     sets it for an existing app. During a rolling upgrade an old apid replica
     writes minutes but not hours. That is the same window as an old replica
     not writing rollups at all, which the coverage section above already
     accepts.
   - The histogram is summed with native `SUM` per bucket, and the CTEs are
     `NOT MATERIALIZED`. The top-groups pass ranks on requests without
     carrying histograms.
   meterd sweeps hourly rows on the same plan ladder, by `bucket_start`.
   On a local Postgres 16 holding 7 days of 80 route and status groups
   (806,400 minute rows), the 7-day route read took 32.5 s from minutes
   alone and 1.3 to 1.9 s with the hourly tier (15,760 rows read).
2. **Sampled measures went missing silently.** The raw read for CPU and the
   latency of sampled rows is bounded by
   `RequestAnalyticsSampledMeasuresTimeout`. Past it the measures were left
   out, and nothing in the response said so. The response now has
   `sampled_measures`: `included`, or `omitted` when any sampled-detail read
   failed or ran out of time. Counts, error rates and percentiles never
   depend on it.
3. **Raw deployment reads were fatal.** The per-deployment cost read and the
   per-route deployment read run on raw rows even when rollups answer. Either
   one failing failed the whole analytics response. Both are now
   best-effort. Beside rollups they share the sampled-measures time budget;
   on raw-row windows they run unbounded. A failure leaves out
   `deployment_costs` or the route deployment splits, logs a warning, and
   sets `sampled_measures` to `omitted`. A cancelled request still fails.

## Amendment 3 (2026-11-07): smaller rollups

A size review found the minute tier holding about 2.8 GB a day for one app
at 100 requests/s (689 bytes per row on Postgres 16, 2,840 rows a minute),
so about 20 GB at Pro's 7-day retention and 39 GB at Scale's 14 days: about
half the raw telemetry it was meant to replace. Two causes, both fixed:

1. **Dimension rows were split by status and deployment.** Every
   `country`, `referrer_host`, `ua_family` and `consumer_id` row carried the
   request's HTTP status and deployment in its key, as route rows do. No
   reader uses either for those kinds: `group_by=status` and the deployment
   splits read route rows only, and error counts are a column. apid now
   writes those rows with status `0` and the nil deployment, so a value is
   one row per minute. Route rows keep their method, status and deployment.
   Migration `20261107164340118` lets the status CHECK of both tiers accept
   `0` on non-route rows (added `NOT VALID`, validated, then the old CHECK
   dropped); the previous release writes rows it accepts and reads status on
   route rows only. Rows written before the upgrade age out with retention;
   readers merge old and new rows of a value as they already merge groups.
2. **The top-N fold was per flush, not per minute.** Each 10-second flush
   kept its own busiest `RequestAnalyticsRollupDimensionsMax` values, so a
   minute stored up to six different sets (about 180 consumers instead of
   50), and each minute row was upserted about six times. apid now writes a
   minute once, at the first flush after it closed
   `RequestAnalyticsRollupMinuteGrace` (30 s) ago, with one fold over the
   whole minute; rows of a minute that arrive later are added by the next
   flush. The grace covers gateways holding a minute until it closes, and
   the minute is still inside `RequestAnalyticsSettleLag`, which readers
   already report as arriving. A clean shutdown writes the open minutes too.
   An apid crash now loses up to about 100 s of rollups instead of 10 s;
   the debugger's raw rows are unaffected. The per-minute value buffer is
   `RequestAnalyticsRollupDimensionsBufferMax` (now 500), as a value costs
   one buffered row.

With the same traffic mix, a minute is now 358 rows written once (538 with a
canary's second deployment), against 2,840 rows from 6,952 upserts: about
8x less storage and 19x fewer row writes, in both tiers. The hourly tier is
derived from the same rows, so each hour row is now updated once a minute
instead of every flush.

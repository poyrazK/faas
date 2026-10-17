# ADR 1536: MCP protocol performance aggregation

Status: accepted

## Context

Starter `mcp_protocol_request` events describe parsed RPC outcomes and separately
measure response construction and SSE delivery. Previously these were searchable
logs without aggregated performance charts or sustained-error alerts.

## Decision

Observe newly committed guest log lines in vmmd, before eviction, replay, or log
stream subscriptions. Boot and restore capture the scheduler's app UUID in the
ring callback. Events cannot supply an app identity; rings without a host app
identity produce no protocol metrics. This boundary establishes attribution, not
truth: a tenant can emit synthetic events for its own app. These metrics are
operational hints, never authorization, billing, admission, or deployment inputs.

A dedicated Prometheus collector retains only counters, cumulative histograms,
and host intake times. It parses at most 4 KiB per line, accepts a closed method
and outcome vocabulary, folds unknown tool names into `other`, and ignores every
argument, result, correlation identifier, and extra field. Labels are app UUID,
reviewed starter tool (`greet`, `add`, `stream_demo`, `build_report`, `other`,
`none`), and a bounded outcome. Non-tool methods use `none`. Only protocol events
count; legacy `mcp_tool_call` diagnostics do not. Duration values must be finite,
nonnegative, and at most one day.

Each host retains at most 1,024 app aggregates, with one hour of inactivity before
expiry. A capacity drop counter reports incomplete ingestion. Process restart
and aggregate expiry reset counters; Prometheus `increase` handles resets and
sums across hosts after calculating each series' increase. New series require
multiple scrapes; counts are estimates rather than an exact request ledger.
Bucket boundaries run from 1 ms to 300 seconds. Longer observations remain in
`+Inf`, with the normal limitations of histogram quantile estimates.

The authenticated `/dashboard/mcp?app=SLUG` view resolves account ownership before
querying by app UUID. A fixed set of parallel queries shares the dashboard read
deadline. It shows one hour of call/failure traffic, execution p95 charts, and
per-tool execution p50/p95/p99 and stream p95. Each chart has at most 61 samples;
missing samples remain gaps. Percentiles require at least 20 observations, using
execution and stream histogram counts separately. Errors and missing source data
produce unavailable states. Last accepted event time measures observation recency,
not a publisher heartbeat: silence can mean idle traffic or missing telemetry.

Shipped Prometheus rules warn after ten minutes of error ratio above 10% or
execution p95 above five seconds. Each five-minute window needs at least 20
observations. RPC errors, tool errors, authorization errors, internal errors, and
timeouts count as failures; cancellations remain separate. RPC or delivery
cancellation increments the cancellation counter once per event, preserving any
successful RPC response outcome. Another rule warns
about sustained aggregation capacity drops. These are platform operator alerts,
not new customer alert-rule API metrics or deployment actions. Silence alone does
not fire an alert. Existing vmmd scrape health alerts cover daemon availability.

## Validation

Collector race tests cover trusted app identity, bounded labels, malformed input,
capacity reclamation, histograms, and concurrent intake/scrapes. Ring/VMM tests
check that intake is observed once and replay does not count again. Query tests
check app scoping, sparse traffic, unavailable reads, and reviewed tool projection.
Dashboard tests cover freshness and missing chart samples. A synthetic promtool
fixture covers sustained firing and sparse/short-lived failures staying quiet.

## Limits

Coverage remains starter-reported parsed RPCs with IDs. It excludes notifications,
pre-dispatch authentication failures, background task execution, lost guest output,
and apps that do not emit this event format. Events are emitted at delivery
completion, so long-lived streams delay their performance observations. Arbitrary custom tools intentionally
share `other`; expanding reviewed labels requires a code change. Custom thresholds,
customer alert subscriptions, and an independent publisher heartbeat are follow-ups.

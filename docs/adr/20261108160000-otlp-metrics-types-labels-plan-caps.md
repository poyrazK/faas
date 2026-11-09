# ADR-20261108160000: Custom metrics take OTLP deltas, up-down counters, histograms and bounded labels, capped per plan

- **Status:** accepted (slices A and B implemented; C to follow)
- **Date:** 2026-11-08
- **Amends:** ADR-202 (pushed custom metrics: the flat cap of 5 names per
  app becomes a per-plan cap), ADR-745 (OTLP metrics ingestion: more types
  accepted), ADR-20261028101615 (admission of new names, unchanged in shape)
- **Related:** ADR-20261108100000 (guest OTLP bridge forwarding and gRPC)

## Context

The 2026-11-07 OpenTelemetry review found custom metrics the weakest part of
Gregale's OpenTelemetry support: five names per app on every plan, only
gauges and cumulative monotonic sums, no data point attributes, and no way
to send metrics from inside the app without an API key (the guest bridge
forces `OTEL_METRICS_EXPORTER=none`). Each custom metric is one latest value
per (app, name) in `app_custom_metrics`; apid exports fresh values to the
platform Prometheus for history, and alerts, release gates and scaling read
a metric by name.

The user decided (2026-11-08):

1. A per-plan cap instead of the flat 5.
2. A small bounded set of labels, with name-only readers aggregating
   (counters summed, gauges by maximum) and an optional label filter.
3. Accept delta sums, up-down counters and histograms.
4. Histograms as derived named series rather than a native histogram kind.
5. The in-VM receiver also accepts metrics.

## Decision

### Slice A — plan caps and types (this change)

- `Limits.CustomMetricsPerApp`: Hobby 20, Pro 50, Scale 200 (Free has no
  custom metrics). Enforcement uses the app's resource owner's plan, so an
  organization's app follows the organization's plan, falling back to the
  caller's. The caps are moderate because schedd reads an app's metrics on
  every tick and apid exports every fresh one (`CustomMetricExportMaxSeries`
  stays 50,000 per scrape). `MaxCustomMetricsPerApp` is now the largest
  per-plan cap.
- Delta monotonic sums are added to a counter's running total; cumulative
  ones replace it as before.
- Non-monotonic sums (up-down counters) are gauges. A delta up-down counter
  is added to the gauge; the stored value is clamped to [0, 1e15] because
  the column's CHECK keeps values non-negative (no migration).
- An explicit-bucket histogram `h` becomes `h_count` and `h_sum` (counters,
  set or added by temporality) and `h_p50`, `h_p95`, `h_p99` (gauges
  interpolated inside the bucket holding the rank, Prometheus
  `histogram_quantile` style, from the newest data point; the first bucket
  starts at the point's min or 0, the overflow bucket ends at its max or the
  last bound). A histogram name may be at most 57 characters so the
  suffixed names fit. Histograms with inconsistent buckets, or negative or
  non-finite bounds or sum, are rejected. Exponential histograms and
  summaries stay rejected.
- Store: `AddCustomMetricOfKind` adds a delta in one statement with the same
  distinct-name admission as `PutCustomMetricOfKind`; a kind change
  restarts the total; the newest `observed_at` is kept.
- Docs recommend delta temporality: several instances sending one counter
  add up as deltas, while cumulative totals replace each other.

### Slice B — in-VM metrics (implemented; opt-in `tracing.metrics`)

The guest bridge serves `/v1/metrics` and `MetricsService/Export`, the
host carries metric frames like trace and log frames, and apid stores them
through the same translation and caps as the public endpoint. With
`tracing.metrics: true` (user decision 2026-11-08: opt-in, because the
managed auto-instrumentation's own metrics would otherwise land on every
traced app's next deploy and fill its cap) Gregale points the metrics
exporter at the bridge with delta temporality unless the app chose one;
without it the exporter stays `none`. `tracing.forward` copies metric
exports too.

- Frames carry `TraceCodecMetricsSignal` (0x40). An older vmmd keeps the bit
  in the codec and refuses the frame as an unknown codec; a vmmd facing an
  apid without `SpansWriter.IngestGuestMetrics` gets Unimplemented and
  refuses permanently, so SDKs never retry into skew.
- vmmd forwards metric frames only for apps with `tracing.metrics`; apid
  checks the owner's plan allows custom metrics, applies its own per-account
  export cap (the log export rate) and stores only for the app's own
  account.

### Slice C — labels (after B)

Each metric name may carry up to 3 label keys and a bounded number of label
combinations; each combination is a series stored and exported with its
labels. Alerts, release gates and scaling rules that name only the metric
read the sum (counters) or the maximum (gauges) across its series, and may
add a label filter to read one series. The design of the series key,
storage and caps is settled in that slice.

## Consequences

- Most OpenTelemetry metric instruments now land without changes to the
  app's SDK configuration; histograms work with existing alerts, gates,
  scaling and history because they are ordinary named metrics.
- A histogram costs five names of the app's cap.
- Cumulative histogram percentiles describe everything since the process
  started, which is rarely what an alert wants; the docs steer to delta.
- Percentiles are estimates bounded by bucket resolution.

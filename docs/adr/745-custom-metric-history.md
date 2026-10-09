# ADR-745 · Custom metric history, OTLP ingestion, charts, and alerts

- **Status:** proposed
- **Date:** 2026-10-09
- **Extends:** ADR-202 (custom application metrics as a scaling signal)
- **Decision:** Turn ADR-202's pushed gauges from a scaling-only, latest-value
  signal into first-class app metrics, in four steps that reuse existing
  machinery:
  1. **History via Prometheus.** apid exports every *fresh* pushed value as
     `gregale_app_custom_metric{app,name}`. The platform Prometheus already
     scrapes apid, so each metric gains 15 days of history with no new table,
     writer, or retention job.
  2. **Read surfaces.** `GET /v1/apps/{slug}/custom-metrics/{name}/series?range=`
     returns the history; the CLI (`gregale metrics <app> --custom <name>`) and
     the app dashboard chart it next to platform metrics.
  3. **OTLP ingestion.** `POST /v1/apps/{slug}/otlp/v1/metrics` on apid
     accepts OTLP gauges and cumulative monotonic sums and applies them as the
     same ADR-202 upserts, so an app already instrumented with OpenTelemetry
     needs only an endpoint and a key. (An earlier draft placed this on
     `gatewayd-public` like trace ingestion; the push endpoint, its
     `metrics:write` keys and the per-app cap all live in apid, and the
     gateway would have needed a new apid RPC to forward each value. OTLP
     exporters accept a full per-signal URL, so the app is scoped by path.)
  4. **Alerts.** A `custom_metric` alert metric names one pushed metric and
     supports both absolute thresholds and ADR-744 baseline comparisons.

  The capability is `internal`, dark behind
  `FAAS_CUSTOM_METRIC_HISTORY_ENABLED=1` until all four slices land; the
  ADR-202 push endpoint and scaling are unaffected by the flag.
- **Why:** ADR-202 left history, charts, and alerting undecided, so a number
  an operator pushes today can drive scaling but cannot be looked at or
  alerted on. App-domain numbers — orders per minute, documents awaiting OCR,
  failed payments — are what teams watch most, and charting and alerting on
  them is a core reason teams adopt external metrics tools. The values are
  already in Gregale; only the read side is missing.
- **Consequences:**
  - **Cardinality** stays bounded by ADR-202's write-time cap: at most
    `apps × MaxCustomMetricsPerApp` series. No labels or attributes in this
    ADR: each attribute value would multiply series per name, and the cap
    would no longer bound Prometheus. OTLP data points carrying attributes are
    rejected with a partial-success count rather than silently merged.
  - **Freshness** follows ADR-202: a value older than
    `CustomMetricFreshnessSeconds` is not exported, so a stopped pusher shows
    as a gap rather than a flat line that looks healthy.
  - **Counters:** OTLP cumulative monotonic sums are stored as their
    cumulative value with `kind = 'counter'`; history and alerts apply
    `rate()` to them. Gauges are stored as-is. The scheduler ignores counters
    as scaling signals, because a cumulative value only grows and would mean
    monotonic scale-out. Negative values stay rejected (ADR-202's CHECK), so
    delta or non-monotonic sums and negative gauges are refused.
  - **Export cost:** the exporter reads at most `apps × MaxCustomMetricsPerApp`
    rows per scrape, cached for the scrape interval. With several apid
    replicas each exports the same series; readers aggregate with `max by
    (app, name)`.
  - **Auth:** OTLP ingestion uses the same `metrics:write` API keys, plan gate,
    and API rate limiting as the existing push endpoint; bodies are capped at
    `OTLPMetricsMaxBodyBytes`.
  - **Alerts** need one nullable `alert_rules.custom_metric_name` column, set
    only when `metric = 'custom_metric'`, enforced by a CHECK.
  - **No DogStatsD in this ADR:** UDP into the control plane is a new network
    path. Apps that already speak StatsD can run the existing OpenTelemetry
    companion preset to convert to OTLP.
- **Rejected alternatives:**
  - *A new time-series table in Postgres.* Duplicates Prometheus, needs
    downsampling and retention jobs, and gives up PromQL (`rate`, ADR-744
    offsets) for alerts.
  - *Prometheus remote write from apid.* Push-based write adds a dependency
    and failure mode where the existing scrape already works.
  - *Accepting attributes now with a series cap.* A cap that rejects new
    attribute values mid-incident is the worst time to lose data; dimensions
    need their own design.

## Slices

1. Exporter (`gregale_app_custom_metric`).
2. Series endpoint, CLI flag, dashboard chart.
3. OTLP metrics ingestion, with the metric kind column it first needs.
4. `custom_metric` alerts, including baseline comparisons.

## Follow-ups

- Per-plan `MaxCustomMetricsPerApp` (today a flat 5).
- Bounded attributes (a small allowlisted set of label keys per metric).

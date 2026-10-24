# ADR-20261022173833 · Custom dashboards

- **Status:** proposed
- **Date:** 2026-10-22
- **Decision:** An account can save named dashboards: an ordered list of
  widgets, each a small, typed reference to a signal Gregale already serves —
  an app's request metric, a log search count, a pushed custom metric, or an
  SLO. Dashboards store only the references; reading one resolves every
  widget for a window through the same code the per-app endpoints use. Dark
  behind `FAAS_CUSTOM_DASHBOARDS_ENABLED`.
- **Why:** The app dashboard shows one app's fixed panels. Teams compose
  their own view across apps — checkout latency next to the payment
  worker's queue depth and the error count from a log search — and that is
  what Datadog dashboards are for. Every signal such a view needs is already
  served per app; what is missing is a saved composition and one read that
  fills it.
- **Consequences:**
  - Widget kinds (closed set):
    - `app_metric` — `app`, `metric` (`request_count`, `error_rate_pct`,
      `latency_p50_ms`, `latency_p95_ms`, `latency_p99_ms`,
      `cold_start_pct`), `window`; read through `appmetrics.Fetch`, the
      code behind `GET /v1/apps/{slug}/metrics`.
    - `log_count` — `app`, `query` (canonical log search filters,
      ADR-20261018083536), `window`; the facet count, with the search
      bounds (24 h for text, 7 d otherwise) and statement timeout.
    - `custom_metric` — `app`, `metric` (a pushed metric name), `window`;
      the ADR-745 series.
    - `slo` — `app`, `slo_id`; the ADR-747 status.
  - Widgets name apps by slug, checked at save time. An app deleted later
    leaves its widgets reading `unavailable`; the dashboard still loads.
  - Bounds in `pkg/api/limits.go`: 20 dashboards per account, 24 widgets
    each, windows `5m`, `1h`, `6h`, `24h`, `7d`. A read resolves widgets
    with bounded concurrency and a per-widget error, so one slow source
    never blanks the rest.
  - Storage: `dashboards` (account, name, widgets jsonb), deleted with the
    account. apid is the only writer.
  - Plan gating follows the sources: a Free account's `app_metric` widgets
    read the same 402 the metrics endpoint returns, as a widget error.
  - Slices: (1) storage, CRUD API, CLI; (2) the data read and
    `gregale dashboards show`; (3) the dashboard page.
- **Rejected alternatives:**
  - *Arbitrary PromQL or SQL widgets.* Cross-tenant risk and unbounded cost;
    every widget kind here maps to an endpoint that is already tenant-scoped
    and bounded.
  - *Storing resolved values.* Dashboards would go stale and duplicate the
    sources.
  - *Per-app dashboards only.* The app dashboard already is one; the gap is
    the cross-app view.

# ADR-747 · Customer-defined SLOs and error budgets

- **Status:** proposed
- **Date:** 2026-10-09
- **Extends:** ADR-082 (customer SLO surface: fixed 99.5% availability,
  `slo_burn_rate` alert)
- **Decision:** Let customers declare their own service level objectives per
  app and track an error budget for each:
  1. **Definitions** (`app_slos`, written by apid). An SLO names an SLI —
     `availability` (non-5xx share of 2xx+5xx responses, ADR-082's
     definition) or `latency` (share of requests completing within a
     threshold) — an objective between 90% and 99.99%, and a rolling
     window of 7 or 30 days.
  2. **Budget history** (`app_slo_hourly`, written by meterd). Each hour
     meterd records the SLO's good and total request counts for the
     completed hour from the gateway's Prometheus series. Missing hours are
     backfilled from Prometheus for up to `SLORollupBackfillHours` (14 days),
     so a Prometheus or meterd outage shorter than that loses nothing.
  3. **Status** (`GET /v1/apps/{slug}/slos/{id}`): attainment and budget
     remaining over the window from the hourly rows, plus 1h and 6h burn
     rates computed live from Prometheus.
  4. **Alerts:** a rule may reference an SLO for multi-window burn-rate
     (ADR-082's 14.4×/6× shape, against the SLO's own objective) or
     budget-remaining thresholds.
- **Why:** ADR-082 gives every app one fixed objective. Teams set their own
  — 99.9% on checkout, p95-style latency targets — and budget their release
  risk against them; error budgets are the reason SLO features exist in
  external monitoring tools. Prometheus keeps 15 days and `request_telemetry`
  3–14 days depending on plan (and is rate-capped and off on Free), so
  neither can answer "how much of this month's budget is left" on its own.
- **Consequences:**
  - **Latency thresholds are a closed set** — the gateway histogram's
    bucket bounds (5 ms … 10 s). A threshold between buckets cannot be
    computed exactly from the histogram, and interpolating it would make
    the budget disagree with the counts it claims to summarise.
  - **App-scoped first.** Per-route SLOs depend on ADR-093 route metrics,
    which are opt-in and collapse past 50 routes into `__route_other__`;
    they are a follow-up once that admission can guarantee an SLO's route a
    series.
  - **Cost:** at most `SLOsPerApp` definitions per app; 24 rows per SLO per
    day, 720 for a 30-day window, purged past the window. Each rollup issues
    two instant PromQL queries per SLO-hour.
  - **Hour granularity:** budget remaining moves in hourly steps; the
    live burn rates cover the current hour.
  - **Ownership:** apid owns definitions (customer intent); meterd owns the
    derived hourly rows, as it owns other usage rollups and the alert
    evaluator.
  - **Plans:** SLOs follow the ADR-082 panel's Hobby+ gate; the per-app cap
    lives in `pkg/api/limits.go`.
- **Rejected alternatives:**
  - *Extending Prometheus retention to 30+ days.* Raises disk for every
    series on the node to serve a handful of SLO sums.
  - *Computing budgets from `request_telemetry`.* Retention, sampling caps,
    and the Free-plan gate all cut the window short.
  - *Arbitrary latency thresholds.* See above; exactness beats flexibility
    for a number customers spend release risk against.

## Slices

1. Definitions: `app_slos`, CRUD API, `gregale slo` CLI.
2. meterd hourly rollup with backfill; status endpoint and `gregale slo status`.
3. SLO alerts (burn rate, budget remaining), dashboard panel, docs.

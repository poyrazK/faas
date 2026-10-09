# ADR-744 · Baseline-relative ("anomaly") alerts for apps

- **Status:** proposed
- **Date:** 2026-10-09
- **Decision:** Let an app alert rule compare a request metric against that
  app's own recent history instead of a fixed number. Two new comparisons,
  `above_baseline` and `below_baseline`, reuse the existing `threshold` column
  as a multiplier: `error_rate_pct above_baseline 3` fires when the error rate
  over the rule's window is at least three times its usual value for that time
  of day. No new columns are added. The capability is `internal`, dark behind
  `FAAS_ANOMALY_ALERTS_ENABLED=1`.
- **Why:** Every customer app alert today is an absolute threshold. A fixed
  1% error-rate alert is too loud for an app whose normal is 0.8% and too
  quiet for one whose normal is 0.01%, and nobody sets thresholds for the
  failure they did not anticipate. Alerting on "unusual for this app" is one
  of the most used features of external monitoring tools, and Gregale already
  holds the history needed to do it with no customer setup.
- **Consequences:**
  - **Metrics:** baseline comparisons are allowed only for the PromQL-backed
    request metrics where history is meaningful and comparable:
    `error_rate_pct`, `latency_p95_ms`, `latency_p99_ms`, `request_count`, and
    `cold_start_pct`. Validation rejects them elsewhere.
  - **Baseline:** the median of the same metric over the same window at the
    same time of day on each of the previous 7 days (PromQL `offset 1d`
    through `offset 7d`, well inside the 15-day retention). The median
    ignores one bad day; same-time-of-day handles daily traffic cycles. At
    least 5 of the 7 days must have data, otherwise the rule is skipped as
    insufficient rather than fired, exactly as other rules skip on degraded
    telemetry.
  - **Cost:** a baseline changes slowly, so the evaluator computes it at most
    once per rule per hour and caches it in memory; a restart only recomputes.
    Each recomputation is 7 bounded queries through the existing
    `FetchAlertMetric` path.
  - **Noise guards** (all in `pkg/api/limits.go`), because ratios on tiny
    numbers are meaningless and scale-to-zero apps are often quiet:
    - the current window must contain at least `AnomalyAlertMinRequests`
      requests (rate and latency metrics);
    - the observed value must also clear a per-metric absolute floor (for
      example an error rate of at least 1%, latency of at least 50 ms), so
      "3× of 0.01%" never pages anyone;
    - a zero baseline never fires `above_baseline`; `below_baseline` on
      `request_count` needs a baseline of at least `AnomalyAlertMinRequests`,
      which turns it into a "traffic dropped" alert.
    - the multiplier is bounded (`above`: 1.5–20; `below`: the fraction
      0.05–0.67) so a rule cannot be configured to fire constantly.
  - **Payload:** the webhook adds `baseline`, `baseline_days`, and `ratio` to
    the existing observed value, so a receiver can say "error rate 4.2%, usual
    1.1% (3.8×)".
  - **Preset:** `error_rate_anomaly` (error rate at least 3× usual over 15
    minutes) joins the preset catalog, behind the same flag.
  - **Schema:** only the `alert_rules_comparison_chk` constraint widens.
- **Rejected alternatives:**
  - *Standard-deviation (z-score) bands.* Request metrics are heavy-tailed and
    often zero for scale-to-zero apps; a multiple of the median is more robust
    and easier to explain in an alert.
  - *A trailing 7-day average instead of same-time-of-day.* Misses daily
    cycles: a normal nightly lull would look like a traffic drop.
  - *Prometheus recording rules per app.* O(apps) rule evaluation in
    Prometheus for every app whether or not it has an anomaly rule; the
    evaluator only computes baselines for rules that exist (ADR-039 keeps
    recording rules O(routes) for the same reason).
  - *Seasonal or ML forecasting.* More accurate in theory, much harder to
    explain and debug, and unnecessary for the first version.

## Follow-ups

1. Implementation: comparison values and validation, baseline fetch with
   cache, guards, payload fields, preset, migration, docs.
2. Show the baseline next to the observed value in the dashboard's alert
   history.
3. Weekly seasonality (same hour on the same weekday) once there is enough
   retention to make it useful.

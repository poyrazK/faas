# FaasMonthlySLO

Covers the `slo_monthly` family (ADR-20261023124955 §3). All are warnings for
the daily review.

| Alert | Fires when |
|---|---|
| `FaasMonthlyApiAvailabilityBelowSLO` | 30-day API availability is under 99.5 % |
| `FaasMonthlyWakeLatencyBelowSLO` | fewer than 95 % of wakes finished within 1 s, so wake p95 > 1 s |
| `FaasMonthlyPlatformWakeGateMissed` | fewer than 95 % of platform wakes finished within 350 ms |
| `FaasMonthlyBuildSuccessBelowSLO` | build success, excluding the customer's own errors, is under 99 % |
| `FaasPrometheusRetentionBelowSLOWindow` | the size cap deletes data before Prometheus holds 31 days |

The ratios come from the `faas_slo_monthly` recording group. They are
rolling 30-day windows over fleet-wide 5-minute rates. The fleet dashboard
shows them in the "API availability (30 d)" row of stat panels.

## Symptom

- **An SLO alert.** The window's error budget is spent: the public number
  for the month is already below target, or will be unless the bad days roll
  out of the window.
  - The fast-burn alerts (`FaasApiAvailabilityBurnRateHigh` and friends) page
    while the budget burns.
  - This alert is the slower "the month is lost" signal.
- **`FaasPrometheusRetentionBelowSLOWindow`.** `prom_retention_size` is deleting
  blocks while the oldest sample is under 31 days old. The 30-day ratios then
  cover only part of a month and can look better or worse than the month
  really was.

## Check

1. **Which days consumed the budget.** Plot the 5-minute rates behind the
   ratio over 30 days:

   ```promql
   faas_slo_api_platform_errors:rate5m / faas_slo_api_eligible_requests:rate5m
   1 - faas_slo_wakes_within_1s:rate5m / faas_slo_wakes:rate5m
   1 - faas_slo_builds_ok:rate5m / faas_slo_builds_eligible:rate5m
   ```

   Match the bad windows against the change timeline: releases, node drains,
   incidents.
2. **Wakes.**
   - `gateway_wake_latency_seconds` includes app start time;
     `gateway_platform_wake_latency_seconds` covers the platform only.
   - If only the first is bad, the slow part is app start inside the guest.
   - If both are bad, `vmmd_restore_phase_duration_seconds` and the wake
     phase events (`gregalectl obs evidence`) name the phase.
3. **Builds.** `sum by (code) (increase(builderd_ops_total{op="build"}[30d]))`
   shows which failure class dominated.
4. **Retention.**
   - `prometheus_tsdb_storage_blocks_bytes` against `prom_retention_size`.
   - `time() - prometheus_tsdb_lowest_timestamp_seconds` for how many days
     are held.
   - `topk(10, count by (__name__) ({__name__=~".+"}))` for the largest
     families.

## Recover

- **SLO alerts:** fix the cause the check found. There is nothing to reset:
  the ratio recovers as the bad days leave the 30-day window. If the
  customer-facing status page will report a miss, post it there with
  `gregalectl status incident create` rather than letting the number speak alone.
- **Retention:**
  - Raise `prom_retention_size` in the operator variables if the disk has
    room. Production-us had 107 GB free on 2026-10-23, with about 28 GB
    projected for 35 days.
  - Otherwise drop bucket series of the largest per-deployment histogram at
    scrape (ADR-20261023124955 §3).
  - Never set `prom_retention_days` under 31; the role refuses it.

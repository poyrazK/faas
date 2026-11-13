# ADR-20261112 · Require monitoring evidence and validate serving responses

- **Status:** accepted for implementation; production rollout remains pending.
- **Date:** 2026-11-12
- **Amends:** [ADR-20261023124955](20261023124955-observability-on-the-control-plane.md)
  §5 (serving probes) and §7 (the observability measurement gate).

## Problem

The observability gate returned exit code zero and `GO` when critical checks
were skipped for missing measurements or a missing baseline. Range queries
could also produce a result from only a small fraction of the requested
measurement window. A compute-node serving probe accepted any 2xx response,
including an unexpected parking page or a response too slow for its purpose.

## Decision

Every gate check requires finite evidence. Missing measurements, missing or
zero latency baselines, an overlapping baseline, or a measurement shorter
than 72 hours return `NO-GO` and exit code one. Short runs remain useful for
diagnosis, but cannot approve production activation.

Required input series must have at least 99% coverage on a one-minute grid
over the measurement window, with current samples too. PostgreSQL stall and
platform wake inputs also require that coverage over the baseline window.
Scrape timestamps must be no older than 120 seconds and no more than 30
seconds in the future. The cgroup collector's own timestamp is checked in
both windows, so repeatedly scraping a frozen textfile cannot establish
fresh evidence. This deliberately assumes a stable qualification fleet;
node churn or replacement series during qualification may require another
measurement window.

Shipper evidence selects the configured Alloy or Promtail on the control
plane (`job="promtail"`); compute shippers cannot satisfy that requirement.
Lazily created drop counters may read as zero only when the local shipper
scrape is healthy, with scrape and read-counter history checked separately.
The gate remains read-only.

Serving probes gain an optional exact UTF-8 body and a first-byte budget.
Both checks participate in the existing two-attempt, failure, recovery and
heartbeat paths. A failing HTTP 200 does not feed the external heartbeat.
The body read is bounded to the expected length plus one byte, at most
4097 bytes, and no expected or observed body is included in messages.
Existing status-only configurations keep their behavior until operators
configure a marker endpoint. Metrics expose whether content validation is
enabled and the selected latency budget.

## Qualification and rollout

- Run `make obs-gate-test serving-probe-test` and the serving-probe promtool
  fixture; validate Ansible configuration before convergence.
- Run the measurement gate with a complete pre-activation baseline:

  ```sh
  scripts/ops/obs_gate.py --window 72h --baseline-end BASELINE_END_RFC3339
  ```

  The baseline must end at or before the measurement window starts. Missing
  traffic that prevents a finite wake percentile requires representative
  qualification traffic, rather than assuming a zero latency baseline.
- Deploy the probe's exact marker response, then configure
  `faas_serving_probe_expected_body` and
  `faas_serving_probe_max_first_byte_seconds` as described in the
  [serving-probe runbook](../ops/serving-probe.md). Keep apps parked between
  runs so probes do not prevent drains.
- Exercise wrong-content and slow-response cases in qualification and verify
  failure, recovery and heartbeat behavior before enabling in production.

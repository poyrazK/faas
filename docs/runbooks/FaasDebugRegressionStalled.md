# FaasDebugRegressionStalled

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `apid_debug_regression_oldest_pass_seconds` (gauge),
`apid_debug_regression_skipped_flag_disabled_total` (counter).
ADR: ADR-127 PR-B (production debugger consumer surface).
Severity: warn (ADR-1236) on `FaasDebugRegressionStalled` (≥26m without a
successful pass). Info on `FaasDebugRegressionDisabledByOperator`
(operator has explicitly turned the regression cron off via
`FAAS_REQUEST_TELEMETRY_ENABLED` — no page, just an annotation
so a customer "why am I not seeing regression banners?" ticket
can be triaged without paging on-call).

## Symptom

The per-app regression detector pass (cmd/apid/debug_regression_cron.go::
runRegressionOnce, ADR-127 PR-B) is failing to keep up. The cron
is a 5m ticker that walks every app that has shipped traffic in
the last 24h, computes a baseline p95 over a 30-min prior-deployment
window, compares to a current p95 over a 30-min current-deployment
window, and upserts a `debug_regression_observations` row for every
(deployment, route) where current.p95 > baseline.p95 * 1.20 AND
the affected count is ≥100. PR-B chose 5m so the dashboard's
"since=10m" filter absorbs one skipped tick; one missed pass is
invisible to the customer.

- **`apid_debug_regression_oldest_pass_seconds` (gauge)**:
  wall-clock seconds since the most recent regression cron pass
  started. A value near the tick interval (300s) means the loop
  is healthy. A value above the tick interval means a pass
  hasn't completed. The Prometheus alert on **staleness** —
  `time() − timestamp(gauge) > 1560` (page, 30m for: 26m =
  5.2x the 5m cadence with 25m slack) — fires when the loop
  hasn't completed a pass in >26m. Note: this metric measures
  pass-staleness (the cron IS running) NOT data-staleness (the
  cron's last write). A regression detector that writes zero
  rows is a positive observation that the loop is healthy; the
  opposite metric would climb indefinitely on a quiet fleet.
- **`apid_debug_regression_skipped_flag_disabled_total` (counter)**:
  bumps once per cron tick when the operator has set
  `FAAS_REQUEST_TELEMETRY_ENABLED` to a falsy value
  (0/false/no/off). The `FaasDebugRegressionDisabledByOperator`
  (info) alert fires when the rate is non-zero for 1h, surfacing
  the deliberate opt-out to the customer's "my regression
  banner is gone" ticket path without paging on-call.
- **Per-app dashboard rendering**: the
  `/dashboard/apps/{slug}/debug` route shows the regression
  banner feed; if the row is empty (no regressions) the banner
  is hidden, so a stalled cron doesn't visually surface as
  "wrong data" — it surfaces as "no banner". Customer-observed
  stalls come from the cron alert firing.

## Likely causes (PR-B chosen: the most common offenders)

1. **Fleet-wide ramp**: a single account with `DebugTelemetryEnabled=true`
   for the first time lands in the cron after a Hobby→Pro upgrade.
   `ListAppsWithRecentTelemetry(24h)` now returns 5x the row set the
   cron was sized for. The cron hits the `debugRegressionMaxApps=1000`
   cap and logs `regression_cron: app cap hit` — the cap is the
   safety valve, not the failure mode.
2. **Per-app route blow-up**: a multi-tenant customer running
   `app.use((req) => req.url)` style routing fans out to one row
   per unique URL in `debug_regression_observations`. The
   `debugRegressionMaxRoutes=200` cap (cmd/apid/debug_regression_cron.go::runRegressionForApp
   on the baseline map) is the safety valve — log line is the
   same `app cap hit` shape with per-app context.
3. **Postgres contention**: a busy fleet that already runs the
   DNS doctor, the audit retention sweep, and the regression
   cron back-to-back can stall the cron's `request_telemetry`
   DISTINCT scan. The `audit_events_*` gauges and the
   `apid_domain_doctor_oldest_observation_seconds` will rise in
   lockstep — look for that sync signal before debugging the
   regression cron in isolation.
4. **Delivery pipeline stopped**: gatewayd-internal's
   `pkg/gateway/request_telemetry_publisher.go` ships every 5s;
   if the publisher's ship queue is backed up the cron won't
   have rows to compare. Check
   `apid_request_telemetry_recorded_total` — a sustained drop in
   `inserted` rate is upstream of the cron stall.
5. **DebugTelemetryEnabled flip** at scale: the operator set
   `FAAS_REQUEST_TELEMETRY_ENABLED=false` to silence false-pages
   during a debug session. The skipped counter rising is the
   expected diagnostic. Stop the experiment → unset the env
   var → restart apid → confirm the loop resumes within 5m.

## Triage (escalate by severity)

1. **Look at the gauge trend in Grafana**: `apid_debug_regression_oldest_pass_seconds`.
   Healthy = oscillating around 300s. Degraded = climbing. Stalled =
   pinned at last value with no further movement (the loop is
   dead or ctx is cancelled).
2. **Check the apid log** for `regression_cron: per-app pass failed`
   — that line carries `(app, err)` so the first failing app is
   visible. The most common error is a Postgres timeout from
   `ListDeploymentsForCompare` hitting its `LIMIT 2` against a
   busy `request_telemetry` partition.
3. **Confirm the cron is alive**: `ss -tlnp | grep apid` should
   show the unix-socket listener for the regression
   gRPC receiver (`cmd/apid/grpc_server_request_telemetry.go`).
   If apid has restarted, the regression cron respawns on the
   next process startup; the loop is in-process and doesn't
   survive a crash.
4. **Diff against main**: this is a PR-B feature. If the alert
   is brand new (a deployment just landed the PR-B merge), give
   it 1h before paging — the metric has no historical baseline
   and the first deploys sometimes set the gauge to "0" via the
   cold-start path, which can look like a long stall when read
   against the 26m for: window on the very first firing.

## Mitigation

1. **No action needed** for a single-tick miss — the 5m cron
   cadence plus the dashboard's `since=10m` default absorbs
   one missed pass without customer-visible impact.
2. **Restart apid** to reset the in-process cron ticker if the
   loop has been wedged for >30m. The state (`debug_regression_observations`)
   is durable; only the in-memory pass cadence is in-process.
3. **Bump `FAAS_REQUEST_TELEMETRY_ENABLED=false`** if the alert
   is sustained and customer-visible regression detection isn't
   a priority for the current incident. This silences the cron
   cleanly (per cron tick the skipped counter increments) and
   surfaces the deliberate opt-out via the info alert.
4. **Open an issue**: if the alert repeats across multiple
   restarts and the apid log shows Postgres timeouts in the
   per-app pass, the hard ceilings need rebalancing. The PR-B
   chosen values (`MaxApps=1000`, `MaxRoutes=200`,
   `DrilldownLimit=10000`, `BaselineWindow=30min`) are
   documented at `cmd/apid/debug_regression_cron.go:67-86`.

## When NOT to escalate

- The alert fires during the **first 1h after a deploy that
  ships this runbook**. The metric has no historical signal;
  the gauge's first read is the cold-start value (0) or a
  stale value from the prior deploy. Set the alert's `for:`
  at ≥30m to absorb this.
- The alert fires during a **planned apid restart** (config
  push, schema migration, debug endpoint offline). The cron
  respawns on the next startup; the gauge returns to the 300s
  oscillation within one tick of the new process lifetime.
- The loop has been **deliberately silenced** by the operator
  (`FAAS_REQUEST_TELEMETRY_ENABLED=false`). The
  `FaasDebugRegressionDisabledByOperator` info alert captures
  the deliberate opt-out — page only when both alerts are
  firing simultaneously, which means the operator's opt-out
  state and the page-fire state are out of sync.

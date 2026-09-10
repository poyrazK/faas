# ADR-039 — traffic anomaly detection on apid (issue #303)

Status: Accepted, 2026-07-26. Owner: @poyrazK. Closes: #303.
Related: #278 (per-customer failure observability, the
`apid_request_failures_total` and `apid_audit_write_failures_total`
counter pair — shares the `accountLabelSet` admission primitive).

## Context

The on-call path for "is anything wrong across the fleet?" stops at
the existing alerts in
`deploy/ansible/roles/prometheus/files/faas.rules.yml` (14 alert
rules, zero recording rules). All of them are absolute-threshold or
rate-threshold alerts. There is no notion of "what does the normal
traffic look like for this route, and how does right now compare" —
a 2x spike at 04:00 UTC on Sunday is a different signal than a 2x
spike at 14:00 UTC on a Tuesday, and the current rules can't tell
them apart.

#303 asks for a 3-day rolling baseline that compares the current 5m
window against the same time-of-day/weekday from the recent past, and
alerts when traffic deviates. The path references in the issue body
(`dashboards/`, `dashboards/slo.json`,
`dashboards/_test/anomaly_test.go`, `apid_request_total`,
`pkg/state/usage_monthly.go`) are illustrative — the actual paths
the work uses are recorded in §Path corrigendum below.

## Decision

Introduce the first recording-rule group in the repo
(`faas_anomaly_baseline`), a new counter (`apid_request_total`), a
new alert family (`traffic_anomaly`), and 4 alerts symmetric on
(metric × direction × mode). Read directly from
`apid_request_total{account_id, route, code}` — paired with the
issue #278 `apid_request_failures_total{account_id, route}` for
the per-account error-rate view.

### Methodology: hybrid (fleet-wide rate + per-account delta)

A 3d trailing average is the baseline for both modes.

- **Fleet-wide**: per route, compare the current 5m rate vs the 3d
  baseline. Catches shared-cause anomalies — DDoS, gateway outage,
  scheduler deadlock, etc. The recording rule fan-out is per route
  (the route label is the only fleet dimension); the ratio is the
  alert input.
- **Per-account**: per account_id, compare the current 5m rate vs
  the account's own 3d baseline. Catches single-tenant brownouts
  (misdeploy, quota runout, customer auth fault) that the fleet
  aggregate would mask. Implemented inline in the alert rules
  rather than as a per-account recording rule — the recording-rule
  set stays O(routes) rather than O(customers) so the rule group
  evaluation cost is bounded by the route table (~30 patterns),
  not by customer cardinality (bounded 10k + `__other__`).

### Alert fan-out: 4 separate alerts, not an umbrella

- `FaasTrafficSpike` (page, fleet, spike): a route > 3x its baseline AND > 1 rps for 10m. Evaluating the per-route vector preserves the route label in the page; the absolute-rate guard prevents a handful of requests against a near-zero baseline from paging.
- `FaasTrafficDrop` (page, fleet, drop): per-route ratio < 0.2 AND rate > 0.1 rps for 15m. The `> 0.1 rps` guard excludes idle routes.
- `FaasTrafficSpikeAccount` (warn, account, spike): per-account ratio > 10 for 15m. Higher threshold than fleet-wide (3x) to suppress per-customer noise.
- `FaasTrafficDropAccount` (warn, account, drop): per-account ratio < 0.1 AND rate > 0.1 rps for 30m. Longer `for:` than the spike rule because quiet customers are common and 30m crosses a real outage from a normal lull.

All four carry `family: traffic_anomaly`, `mode: fleet|account`,
`direction: spike|drop`. The `family` label keeps the existing
`family`-based inhibition / silencing rules composed (page-tier
suppresses warn-tier only when the alertnames match otherwise). A
single runbook (`FaasTrafficAnomaly.md`) branches on `{{ $labels.mode }}`
and `{{ $labels.direction }}`.

### Recording rule namespace: `faas_apid_<metric>_<operation>:by_route`

The Prometheus convention is `level:metric:operation` — here the
"level" is `faas`, the "metric" is `apid_<aspect>_<time-or-state>`,
the "operation" is the recording context (`by_route`). The
3d-baseline series are `faas_apid_request_rate_3d_baseline:by_route`
and `faas_apid_error_rate_3d_baseline:by_route`; the 5m series are
`faas_apid_request_rate_5m:by_route` and
`faas_apid_error_rate_5m:by_route`; the ratios are
`faas_apid_request_rate_ratio:by_route` and
`faas_apid_error_rate_ratio:by_route`.

`clamp_min(..., 1)` on the error-rate denominator prevents /0 when
a route has zero recent traffic; `clamp_min(..., 0.001)` on the
ratio floor lets the formula produce a finite value rather than
NaN. Both are the conventional Prometheus pattern.

### `code` label on `apid_request_total`

`apid_request_total{account_id, route, code}` adds a `code ∈ {ok,
err}` label so the error-rate recording rules can read from the
same counter. This is a deliberate asymmetry from
`apid_request_failures_total{account_id, route}` (no `code` since
failures are by definition `err`) and is reconciled in a follow-up
PR — see §Consequences.

The `code` label is derived from the response status via
`wire.CodeFromStatus(status int) string` (2xx/3xx → "ok", 4xx/5xx
→ "err"). The function mirrors the existing
`observeErrFromStatus` helper in `cmd/apid/server.go:821-825` (which
feeds `apid_ops_total{code}`), so the two counters report
consistent client/server views.

### Retention

`prom_retention_days: 15` (deploy/ansible/roles/prometheus/defaults/main.yml:15)
gives 5x headroom over the 3d baseline window. A 3d baseline is
feasible within retention.

### `account_id` cardinality

The new `apid_request_total{account_id, route, code}` shares the
existing `accountLabelSet` admission primitive (issue #278,
`pkg/wire/metrics.go:1036`): fixed-capacity 10 000, non-evicting,
reserved `anonymous` + `__other__`. The three per-customer metrics
(`apid_request_total`, `apid_request_failures_total`,
`apid_audit_write_failures_total`) read through the same
`m.accountLabel(id)` helper, so a customer is represented by their
real id in all three, or by `__other__` in all three. The
`__other__` overflow bucket is annotated explicitly on every
account-level alert so on-call knows to grep the daemon slog for
the original id.

### Test patterns

Three new artifacts, one extension. The repo has no precedent for
PromQL-engine tests; the patterns below are adapted from the
closest existing conventions (`pkg/wire/metrics_test.go:19-54` for
scrape-based asserts; `pkg/meter/residency_test.go:67-104` for the
metric-production shape; `cmd/apid/status_test.go:42-51` for HTTP
PromQL fixtures).

- **Wire-layer** (extend `pkg/wire/metrics_cardinality_test.go`):
  `TestRequestTotalSharesAdmissionSet` asserts the new counter
  shares the `accountLabelSet` with the existing failure counter.
  `TestRequestTotalOverflowsToSharedOther` asserts the
  10 001st id collapses to `__other__` in both.
  `TestRequestTotalForExtractsRouteAndCode` pins the
  route-from-Pattern + code-from-status extraction.
  `TestCodeFromStatus` is a unit test for the helper.
  `TestRequestTotalPreInstantiated` asserts the closed
  (account_id, route, code) tuples surface from the first scrape
  so the §12 panels and alerts never see "no data" on an idle box.
- **Rule-syntax** (new, `pkg/promqlrules/rules_test.go`,
  `//go:build integration`): shells out to `promtool check rules` as
  a subprocess. Skips if `promtool` is not on PATH; CI installs it
  explicitly via the workflow step at
  `.github/workflows/ci.yml:113-135`. This is a new pattern for the
  repo; future recording rules can mirror the same shape.
- **Dashboard JSON** (new, `pkg/grafana/dashboard_test.go`):
  narrow Go test that asserts the 4 new panel IDs (80, 81, 82, 83)
  are present in `deploy/grafana/faas-fleet.json` with non-empty
  `expr` on the first target. A second test asserts the dashboard
  is byte-identical to the role-copied file at
  `deploy/ansible/roles/grafana/files/faas-fleet.json` — the
  Ansible role copies the role file onto the box, so drift between
  the two silently breaks the dashboard provisioning.

## Consequences

- **Positive**: an operator looking at a `FaasTrafficSpike` page can
  see the offending route, the ratio, and drill down to a single
  customer via the dedicated top-N panel. The 3d baseline
  eliminates false positives driven by time-of-day / weekday
  seasonality.
- **Positive**: the new metric (`apid_request_total`) and
  recording-rule set (7 rules) are the canonical inputs to a future
  capacity-planning feature (per-app, per-region). The §12
  dashboard surface is reusable without rework.
- **Positive**: the `family: traffic_anomaly` label composes with
  the existing alertmanager inhibition / silencing rules. A
  future tier-2 change to silence on maintenance windows can
  target the family without changes to this PR.
- **Negative**: `promtool` is required to validate the rule
  changes locally. The `pkg/promqlrules/rules_test.go` integration
  test makes this explicit; the unconditional CI step at
  `.github/workflows/ci.yml:113-135` catches the validation gap.
- **Negative**: the 8-alert fan-out (4 page alerts from PR #336 +
  `FaasTrafficAnomaly`, `FaasErrorRateSpike`, `FaasErrorRateDrop`
  from the follow-up + `FaasTrafficSpikeAccount`/`Drop` warn pair)
  is wider than the existing alert set. On-call may need a few
  weeks to internalize the page-vs-warn split. The runbook
  branches explicitly on `mode` and `direction` to make the
  page-content distinct.

## Path corrigendum (issue #303 body)

The issue's path references don't match the repo. The actual paths
the work uses:

| Issue body | Actual |
|---|---|
| `dashboards/slo.json` | `deploy/grafana/faas-fleet.json` (byte-identical copy at `deploy/ansible/roles/grafana/files/faas-fleet.json`) |
| `dashboards/_test/anomaly_test.go` | `pkg/grafana/dashboard_test.go` (new package; the repo has no `dashboards/` directory) |
| `apid_request_total` | new — added in this PR as `apid_request_total{account_id, route, code}` |
| `pkg/state/usage_monthly.go` | not used. `usage_monthly` is a Postgres **view** at `migrations/00002_app_manifest_and_domains.sql:58-68` (a billing rollup from `usage_minutes`, not a traffic counter) |

The issue body is treated as illustrative; the work uses the actual
paths. This corrigendum is the only place in the repo that
explicitly records the deviation.

## Open follow-ups (not blocking)

- **Per-app / per-region labels**: not in scope. Per-app would
  multiply series by the app count; per-region is a single-node
  platform so it doesn't apply.
- **Postgres-side anomaly detection**: out of scope — the
  `usage_monthly` view is a billing rollup, not a traffic counter.
  Future billing-anomaly work would be a separate ADR.
- **Multi-window correlation** (hour + day + week baselines):
  future enhancement. The 3d baseline is the load-bearing minimum.

## Cross-references

- ADR-031 — self-hosted Grafana (panel provenance, alertmanager
  inhibition patterns).
- ADR-035 — auth audit events (the `auditWriteFail` counter and
  the `result` label closure pattern are reused for the
  `code` label).
- ADR-036 — per-instance metric cardinality rollups (the
  `accountLabelSet` non-evicting admission is the
  cardinality-bound primitive this work reuses).
- ADR-037 — reactive scale-up trigger (the
  `schedd_scale_up_admit_rps` histogram is the per-instance RPS
  precedent).
- Issue #278 — per-customer failure observability
  (`apid_request_failures_total`, `apid_audit_write_failures_total`,
  `accountLabelSet`). The `code` label deviation is reconciled in
  a follow-up.
- Issue #303 — the issue this ADR closes.

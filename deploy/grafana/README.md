# Grafana dashboard — `faas-fleet.json`

Grafana 11 export. Panels cover all 7 of the spec §12 dashboard rows
that are scorable today; one row remains deferred (rationale below).

## `bridge-protection.json` (ADR-127 §D3 / G19)

Four-panel dashboard for the bridge wire-protocol framing protection
surface: framing rate by (app_protocol, bridge_protocol, framing) / 5m
(`vmmd_bridge_framing_total`), framing MISMATCH rate (the alert source
for `FaasBridgeFramingMismatch` in `bridge.rules.yml`), active
bridge_protocol=h1 count on http2/grpc apps (the surgical-rollback
indicator), and bridge H2C handshake latency p99
(`vmmd_op_duration_seconds_bucket{op="bridge_h2c_roundtrip"}` — the
histogram lands with a follow-on). UID `faas-bridge-protection-adr-127`.
Companion alerts at `deploy/ansible/roles/prometheus/files/bridge.rules.yml`
under `family: bridge` (FaasBridgeFramingMismatch + FaasBridgeRollbackStuck).
Runbook: `docs/ops/h2c-rollback.md`.

## `warm-snapshot.json` (issue #470 / PR C / ADR-074)

Four-panel dashboard for the warm-snapshot tier ops surface: warm-capture
errors per reason (`*_warm_snapshot_errors_total{reason}`), guest-init
duration p50/p95 by app (`vmmd_guest_init_duration_seconds_bucket`),
wake-tier mix stacked (`schedd_wake_snapshot_tier_total{tier}`), and
snapshot-population-by-tier stat panel. UID `faas-warm-snapshot-pr-c`;
land via `dashboards/warm-snapshot.json` import after the ansible role
provisions the fleet dashboard (PR #141, ADR-031).

## `edge-rules.json` (issue #561 / PR B / ADR-091)

Four-panel dashboard for the edge-rules observability cluster: match
rate by kind (`gateway_edge_rule_match_total{outcome="match"}`), apply
rate by kind + result (`gateway_edge_rule_apply_total`, green=success,
red=error), JWT failure rate (`gateway_edge_rule_match_total{kind="jwt",outcome="failed"}`),
and compile-error stat panel (`gateway_edge_rule_compile_error_total{kind}`
— any non-zero paints red, a page-tier signal because a rule shipped
broken is an actionable correctness signal, not a headroom signal).
UID `faas-edge-rules-pr-b`. Mirror at
`deploy/ansible/roles/grafana/files/edge-rules.json` (byte-identical —
`make grafana-mirror-check` enforces the contract). Runbooks in
`docs/runbooks/`: `FaasEdgeRuleApplyHigh.md`, `FaasEdgeRuleCompileError.md`,
`FaasEdgeRuleJWTFailures.md`.

## `audit-retention.json` (ADR-091 D20.4 / PR-D20.4, ADR-075)

Three-panel dashboard for the audit-event retention observability
chip. Closes the operator surface for the three prerequisite
metrics shipped by PR-B (PR #857) in `pkg/wire/metrics.go:1389-1425`:
`apid_audit_events_deleted_total` (counter, only ticks on passes
where `deleted > 0` per `pkg/eventretention/cleanup.go:205-210`),
`apid_audit_events_retention_lag_seconds` (gauge, reads ~90d on every
healthy pass because `cleanup.go:198-199` sets cutoff = now − 90d and
line 211-213 Sets the gauge to now − cutoff), and
`apid_audit_events_volume_total{kind_prefix}` (counter, labelled).

Panels mirror `docs/runbooks/FaasAuditRetentionExhaustion.md`'s
three-signal triage:

1. **Audit events pruned / 24h** — `sum(increase(apid_audit_events_deleted_total[24h]))`. Idle days (loop running, nothing past the cutoff) read 0; the counter is `Add`-ed only when `deleted > 0`.
2. **Retention lag (days, healthy: 89-91)** — `apid_audit_events_retention_lag_seconds / 86400`. Stat panel with green band 89-91d, yellow 91-95d, red <89 or >95d. The healthy value is exactly 90d on every pass; values outside the band mean clock skew or `CutoffDays` misconfiguration.
3. **Audit-event volume by kind_prefix (top 8)** — `topk(8, sum by (kind_prefix) (rate(apid_audit_events_volume_total[5m])))`. The `topk(8, ...)` clamp survives the bounded-admission helper at `pkg/wire/metrics.go` (overflow collapses to `__other__`).

UID `faas-audit-retention-d20-4`. Mirror at
`deploy/ansible/roles/grafana/files/audit-retention.json`
(byte-identical — `make grafana-mirror-check` enforces the contract).
Companion Prometheus alerts live at
`deploy/ansible/roles/prometheus/files/faas.rules.yml` under
`family: audit_retention`:

- `FaasAuditRetentionLoopStalled` (page) — `time() − timestamp(gauge) > 93600` (26h, one cadence + 2h slack) for 30m. SOC 2 CC6.2 retention obligation at risk.
- `FaasAuditRetentionLoopStretched` (warn) — `> 180000` (50h, 2x cadence + 2h slack) for 30m. Loop is alive but cadence is broken.
- `FaasAuditRetentionTableGrowingFasterThanPruned` (warn) — volume rate > 0 AND `(volume_rate − deleted_rate) > 100` rows/day for 6h. The `> 0` precondition prevents firing on idle days when the deleted counter is flat.

Runbook: `docs/runbooks/FaasAuditRetentionExhaustion.md`.

## `audit-write-fidelity.json` (PR #1111 / Obs-Meta + Trace-IDs follow-on)

Four-panel dashboard for the audit-write fidelity surface shipped by
PR #1111 C2: `<daemon>_audit_log_write_total{endpoint, kind}` and
`<daemon>_audit_log_write_failures_total{endpoint, kind, error_class}`
counters (per-daemon prefix, counter declarations at
`pkg/wire/metrics.go:1510 / 1515`, pre-instantiation grid at lines
3215-3222 spanning `auditEndpointClosedSet` × `auditKindClosedSet`
× `auditErrorClassClosedSet` — declarations at lines 3158 / 3159 /
3178). The audit emit is best-effort by design (ADR-035) — a
sustained non-zero failure rate means audit rows are silently
missing while customer traffic looks healthy.

Panels:

1. **Audit writes / 5m by endpoint** — `sum by (endpoint) (rate({__name__=~".*_audit_log_write_total"}[5m]))`. Stacked across the auditEndpointClosedSet (apid / schedd / meterd / gatewayd-internal).
2. **Audit write failures / 5m by error_class** — `sum by (error_class) (rate({__name__=~".*_audit_log_write_failures_total"}[5m]))`. Drill-down shape the FaasAuditWriteFailuresSpike runbook opens with.
3. **Audit write failure ratio (5m)** — failures / clamp_min(total, 0.001) with green<0.001 / yellow<0.01 / red≥0.01 threshold bands.
4. **Audit writes by kind (top 8)** — `topk(8, sum by (kind) (rate({__name__=~".*_audit_log_write_total"}[5m])))`. The bounded-admission helper at `pkg/wire/metrics.go` collapses overflow to `__other__`.

UID `faas-audit-write-fidelity-pr-1111`. Mirror at
`deploy/ansible/roles/grafana/files/audit-write-fidelity.json`
(byte-identical — `make grafana-mirror-check` enforces the contract).
Companion alert at
`deploy/ansible/roles/prometheus/files/faas.rules.yml` under
`family: audit_write`:

- `FaasAuditWriteFailuresSpike` (warn) — `sum(rate({__name__=~".*_audit_log_write_failures_total"}[5m])) > (5/60)` for 5m. Cross-daemon, `component=platform`. Deliberately not paired with the existing apid-only `FaasApidAuditWriteFailures`: the load-bearing reason is that BOTH are `severity=warn` and the alertmanager inhibit rule (`alertmanager.yml.j2:124-136`) requires `source.severity=page` to engage — no page-tier `audit_write` alert exists, so the cross-daemon warn stays independently visible regardless of component labels. Component labels route the email (platform-wide vs apid-only queue); severity is the inhibit lever.

Runbook: `docs/runbooks/FaasAuditWriteFailuresSpike.md`.

## `obs-trace-completeness.json` (PR #1111 / Obs-Meta + Trace-IDs follow-on)

Four-panel dashboard for the obs-meta trace completeness surface
shipped by PR #1111 C2-C4: the schedd gauge
`schedd_operator_action_trace_completeness_ratio{kind}` set by
`pkg/sched/operator_intent_completeness.go::observeOperatorIntentCompleteness`
on a 60s tick. Every panel filters on `schedd_…` because the gauge
is registered on every daemon (gauge constructor at
`pkg/wire/metrics.go:3211`, pre-instantiation at 3232-3233) but
ONLY schedd's driver calls `Set` — cross-daemon queries mix real
values with never-set/zero defaults on other daemons.

Panels:

1. **Operator action trace completeness by kind (5m)** — `clamp_min(schedd_operator_action_trace_completeness_ratio, 0.001)`. Per-kind ratio timeseries with green≥0.95 / yellow 0.50-0.95 / red<0.50 threshold bands. `clamp_min(0.001)` mirrors the `vmmd_cold_boot_ratio` recording-rule precedent so the y-axis is bounded away from the `WithLabelValues(...)` default of 0 before the driver's first pass.
2. **Operator action audit write rate by kind (denominator)** — `topk(8, sum by (kind) (rate({__name__=~".*_audit_log_write_total"}[5m])))` stacked. Disambiguator for the vacuous-truth default of 1.0 (absent kinds read 1.0 by `pkg/sched/operator_intent_completeness.go:172-179`). A kind whose ratio sits at 1.000 with non-zero write rate is a real violation; a kind whose ratio sits at 1.000 with flat write rate is a vacuous default. Same precedent as `FaasAuditRetentionTableGrowingFasterThanPruned`'s `> 0` precondition at `faas.rules.yml:463-469`.
3. **Driver loop freshness (s, healthy <180)** — `time() - schedd_operator_action_trace_completeness_last_success_timestamp_seconds`. The producer writes the timestamp only after a successful query, so scrape freshness cannot mask a wedged driver. Stat with green<180 / yellow<360 / red≥360.
4. **Worst-kind completeness ratio** — recording rule `obs:operator_action_trace_completeness_ratio` (the cold-start-guarded expression — see the `Recording rule` paragraph below the alert list).

UID `faas-obs-trace-completeness-pr-1111`. Mirror at
`deploy/ansible/roles/grafana/files/obs-trace-completeness.json`
(byte-identical — `make grafana-mirror-check` enforces the contract).
Companion alerts at
`deploy/ansible/roles/prometheus/files/faas.rules.yml` under
`family: obs_trace`:

- `FaasOperatorActionTraceCompletenessLowPage` (page) — `< 0.50` for 10m. Cross-daemon correlation is broken when audit rows lack `trace_id`.
- `FaasOperatorActionTraceCompletenessLowWarn` (warn) — `< 0.95` for 30m. Above the page threshold but below the contractual floor.
- `FaasOperatorActionTraceCompletenessLoopStalled` (info) — producer last-success timestamp stale >180s for 5m. Schedd driver goroutine wedged after at least one successful tick.
- `FaasOperatorActionTraceCompletenessFirstTickStalled` (page) — no successful first tick for 10m. Covers a pre-Set panic or a driver blocked before publishing any completeness value.

All four share `family=obs_trace, component=schedd` so the alertmanager inhibit rule auto-pairs page+warn.

Recording rule: `obs:operator_action_trace_completeness_ratio` —
`clamp_min((min(schedd_…) unless on()
(schedd_operator_action_trace_completeness_first_tick_completed_total == 0))
or on() vector(1), 0.001)`. The explicit first-success counter is the
cold-start guard: before the driver completes its first query, the rule
substitutes 1.0 (vacuous truth) so restart noise does not page. Once a
query succeeds, a real all-zero result is no longer hidden. The separate
first-tick page covers a pre-Set panic or a driver that cannot complete its
initial query.

Regression test: `pkg/promqlrules/testdata/obs_trace_completeness.test.yml`
(Go-level driver at `pkg/promqlrules/rules_test.go:74` walks the
testdata dir under `-tags=integration`). Same shape as the
`vmmd_cold_boot_ratio` precedent at `faas.rules.yml:1147`, no
`:Nm` time-window suffix because the upstream gauge is instant
rather than counter-rate.

Runbook: `docs/runbooks/FaasOperatorActionTraceCompletenessLow.md`.

## `telemetry-pipeline.json` (OTLP exporter health)

Eight-panel dashboard for both daemon-owned telemetry exporters. The first
four panels show the Prometheus-to-OTLP metrics bridge; the next four show
configured/up trace exporters, trace export failures, and age since the last
successful trace export. Both bridges remain best-effort: local Prometheus
scraping and daemon serving are not gated on collector availability.

UID `faas-telemetry-pipeline`. Mirror at
`deploy/ansible/roles/grafana/files/telemetry-pipeline.json` (byte-identical —
`make grafana-mirror-check` enforces the contract). Companion alerts are
`FaasOTLPMetricsExporterDown`, `FaasOTLPMetricsExporterErrors`,
`FaasOTLPTraceExporterDown`, and `FaasOTLPTraceExporterErrors` in
`deploy/ansible/roles/prometheus/files/faas.rules.yml`.

Runbook: `docs/runbooks/FaasOTLPMetricsExporterDown.md`.

Trace exporter runbook: `docs/runbooks/FaasOTLPTraceExporterDown.md`.

## `loki-pipeline.json` (issue #274 follow-up)

The Loki pipeline dashboard covers both control-plane and compute Promtail
targets, plus direct backend availability, retention-sweep age, source
ingestion, successful sends, and dropped entries. The Prometheus role must be
configured with `prom_loki_metrics_target` for the backend panels and alerts
to have data. The dashboard is mirrored at
`deploy/ansible/roles/grafana/files/loki-pipeline.json`; `make
grafana-mirror-check` enforces byte identity.

When `gv_loki_url` is configured, the Grafana role also provisions a `Loki`
datasource using provider-owned mTLS PEM values from
`/etc/grafana/secrets/loki.env`. This makes the health panels and Grafana
Explore use the same private transport and tenant boundary as Promtail.

## Provisioning (PR #141, ADR-031)

The canonical install path is `deploy/ansible/roles/grafana/`, which
apt-installs Grafana OSS, SHA-256-pins the binary, provisions the
Prometheus datasource + this JSON from disk, and binds the management
bridge on `10.0.0.1:3000`. Run `make bootstrap` against a reference node to
provision; the dashboard lands at
`/d/faas-fleet-m8/faas-fleet-m8-12`.

For a hand-import path (developer laptop, external Grafana instance):

1. Open Grafana → Dashboards → Import.
2. Upload `faas-fleet.json`.
3. Select your Prometheus datasource (must be named or aliased
   `prometheus` — Grafana's import rewrites the datasource UID).
4. The dashboard lands at `/d/faas-fleet-m8/faas-fleet-m8-12`.

## Scrape source

The dashboard reads from the local Prometheus installed by
`deploy/ansible/roles/prometheus`. The scrape config there
(`prometheus.yml.j2`) targets every Gregale daemon + node_exporter on
the bridge IP. No remote source — the dashboard is single-node today (Tier A
will move to a federated scrape per ADR-031).

## Panels

| Panel | Metric | Spec §12 row |
|---|---|---|
| Wake latency p50 / p95 | `gateway_wake_latency_seconds` | wake latency |
| Wake queue wait p95 | `gateway_wake_queue_wait_seconds` | wake queue wait |
| Cold-boot fallback rate | `vmmd_cold_boot_fallback_total` / Σ(vmmd_ops_total{op=~"CreateFromSnapshot|CreateColdBoot"}) | cold-boot fallback rate |
| Snapshot fleet avg / p95 (MB) | `fcvm_snapshot_fleet_avg_bytes`, `…_p95_bytes` | snapshot fleet avg |
| Resident RAM % | `fcvm_resident_ram_pct` | resident_ram_pct_of_target |
| lv-fc used % | `fcvm_lv_fc_used_pct` | lv-fc utilisation |
| Wake rate | `gateway_requests_total` | — (operator sanity) |
| Edge rule apply rate | `gateway_edge_rule_apply_total{kind,result}` | edge rule apply rate |
| Edge rule compile errors | `gateway_edge_rule_compile_error_total{kind}` | edge rule compile errors |
| Build success rate (non-user_error) | `builderd_ops_total{op="build"}` | build success |
| Build queue wait p95 | `builderd_build_queue_wait_seconds` | build queue wait p95 |
| Build duration p95 (by outcome) | `builderd_build_duration_seconds` | per-outcome wall-clock |
| API availability (5m) | `gateway_requests_total{code=~"2.."}` / `gateway_requests_total` × 100 | public SLO |
| Resident GB per paying customer | `meterd_resident_gb_per_customer{plan}` | resident GB per paying customer |
| Per-route top 10 reqps + error rate (ADR-093) | `faas_gateway_request_rate_5m:by_route`, `faas_gateway_error_rate_5m:by_route` | per-route breakdown (opt-in) |
| Per-route top 10 p95 latency (ADR-093) | `faas_gateway_p95_seconds:by_route` | per-route p95 (opt-in) |
| Deployment cancel rate by outcome (5m) | `apid_ops_total{op="deployment_cancel",outcome}` | queue controls — cancel (ADR-124) |
| Deployment reorder rate by outcome (5m) | `apid_ops_total{op="deployment_reorder",outcome}` | queue controls — reorder (ADR-124) |
| Deployment clear (single) rate by outcome (5m) | `apid_ops_total{op="deployment_clear",outcome}` | queue controls — clear (ADR-124 + PR #1181) |
| Deployment clear-obsolete (bulk) rate by outcome (5m) | `apid_ops_total{op="deployment_clear_obsolete",outcome}` | queue controls — clear-obsolete (ADR-124 + PR #1181) |
| Deployment queue controls — overall success rate (5m) | `apid_ops_total{op=~"deployment_(cancel\|reorder\|clear\|clear_obsolete)",outcome="ok"}` / same set, all outcomes | §12 success-rate tile across all 4 ops |
| Compute metrics scrape coverage | `faas_compute_metrics_scrape_coverage:by_job` | HTTP-SD downstream coverage (issue #1219 follow-up) |
| Compute metrics scrape health by node | `up{job=~"gatewayd-internal\|promtail-compute"}` | HTTP-SD downstream target reachability |
| Compute metrics scrape duration by node | `scrape_duration_seconds{job=~"gatewayd-internal\|promtail-compute"}` | HTTP-SD downstream scrape latency |

## Deferred rows

- **Per-app SLO row** — the per-app p95 wake + 5xx rate are too
  high-cardinality for the fleet-level dashboard. They live on the
  status page instead (see `deploy/statuspage/index.html`).

## Source of truth

`docs/faas_implementation_spec.md` §12 lists every dashboard row.
Renames must update the spec first, then the metric, then this
dashboard — never the other way around.

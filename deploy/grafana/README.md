# Grafana dashboard — `faas-fleet.json`

Grafana 10 export. Panels cover 5 of the 7 spec §12 dashboard rows
that are scorable today; the other two are deferred (rationale below).

## Import

1. Open Grafana → Dashboards → Import.
2. Upload `faas-fleet.json`.
3. Select your Prometheus datasource (must be named or aliased
   `prometheus` — Grafana's import rewrites the datasource UID).
4. The dashboard lands at `/d/faas-fleet-m8/faas-fleet-m8-12`.

## Scrape source

The dashboard reads from the local Prometheus installed by
`deploy/ansible/roles/prometheus`. The scrape config there
(`prometheus.yml.j2`) targets every faas daemon + node_exporter on
the bridge IP. No remote source — the dashboard is single-box.

## Panels

| Panel | Metric | Spec §12 row |
|---|---|---|
| Wake latency p50 / p95 | `gateway_wake_latency_seconds` | wake latency |
| Wake queue wait p95 | `gateway_wake_queue_wait_seconds` | wake queue wait |
| Cold-boot fallback rate | `vmmd_cold_boot_fallback_total` | cold-boot fallback rate |
| Snapshot fleet avg / p95 (MB) | `fcvm_snapshot_fleet_avg_bytes`, `…_p95_bytes` | snapshot fleet avg |
| Resident RAM % | `fcvm_resident_ram_pct` | resident_ram_pct_of_target |
| lv-fc used % | `fcvm_lv_fc_used_pct` | lv-fc utilisation |
| Wake rate | `gateway_requests_total` | — (operator sanity) |
| Build success rate (non-user_error) | `builderd_ops_total{op="build"}` | build success |
| Build queue wait p95 | `builderd_build_queue_wait_seconds` | build queue wait p95 |

## Deferred rows

- **resident_gb_per_paying_customer** — needs a nightly PG aggregate
  (`SUM(ram_mb+8) GROUP BY owner`) joined to a paying-customer rollup.
  Defer until meterd (M7) emits a per-customer timeseries and we
  provision `pg_exporter` to land it in Prometheus.
- **Per-app SLO row** — the per-app p95 wake + 5xx rate are too
  high-cardinality for the fleet-level dashboard. They live on the
  status page instead (see `deploy/statuspage/index.html`).

## Source of truth

`docs/faas_implementation_spec.md` §12 lists every dashboard row.
Renames must update the spec first, then the metric, then this
dashboard — never the other way around.
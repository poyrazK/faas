# Grafana dashboard — `faas-fleet.json`

Grafana 11 export. Panels cover all 7 of the spec §12 dashboard rows
that are scorable today; one row remains deferred (rationale below).

## Provisioning (PR #141, ADR-031)

The canonical install path is `deploy/ansible/roles/grafana/`, which
apt-installs Grafana OSS, SHA-256-pins the binary, provisions the
Prometheus datasource + this JSON from disk, and binds the management
bridge on `10.0.0.1:3000`. Run `make bootstrap` against the EX44 to
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
(`prometheus.yml.j2`) targets every faas daemon + node_exporter on
the bridge IP. No remote source — the dashboard is single-box.

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
| Build success rate (non-user_error) | `builderd_ops_total{op="build"}` | build success |
| Build queue wait p95 | `builderd_build_queue_wait_seconds` | build queue wait p95 |
| Build duration p95 (by outcome) | `builderd_build_duration_seconds` | per-outcome wall-clock |
| API availability (5m) | `gateway_requests_total{code=~"2.."}` / `gateway_requests_total` × 100 | public SLO |
| Resident GB per paying customer | `meterd_resident_gb_per_customer{plan}` | resident GB per paying customer |

## Deferred rows

- **Per-app SLO row** — the per-app p95 wake + 5xx rate are too
  high-cardinality for the fleet-level dashboard. They live on the
  status page instead (see `deploy/statuspage/index.html`).

## Source of truth

`docs/faas_implementation_spec.md` §12 lists every dashboard row.
Renames must update the spec first, then the metric, then this
dashboard — never the other way around.
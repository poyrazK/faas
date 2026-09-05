# Alert cookbook

This is the operator-facing index of alert families, the
runbook each family routes to, and the common
false-positive scenarios that an oncall should rule out
before paging further.

The actual alert rules live in
`deploy/ansible/roles/prometheus/files/faas.rules.yml`
and `deploy/ansible/roles/prometheus/files/pg_backup.rules.yml`.
The dispatch path is `prometheus → alertmanager →
faas-page route → Pushover + email → primary oncall`.

## How to use this cookbook

When you receive a page:

1. Read the alert name from the page notification.
2. Find the alert in the table below.
3. Follow the "First read" link to the alert-specific
   runbook (if it exists) or the family-level runbook.
4. Check the "Common false positives" column BEFORE
   paging Tier 1 — many alerts fire on transient
   conditions that resolve themselves within 5-15
   minutes.

## Alert family index

| Alert family | Severity | First-read runbook | Common false positives |
|---|---|---|---|
| `tenant_residency` | page | [`FaasHighResidentRam.md`](FaasHighResidentRam.md) | None — this is always a real signal. |
| `tenant_residency` | warn | [`FaasHighResidentRam.md`](FaasHighResidentRam.md) | A single customer's burst can spike the gauge transiently; check `faas_resident_ram_pct{account_id}` to see if one tenant is driving the alert. |
| `snapshot_fleet` | page | [`FaasSnapshotFleetHigh.md`](FaasSnapshotFleetHigh.md) | None — fleet-wide storage pressure is always actionable. |
| `lv_fc` | warn | [`FaasLvFcUsageHigh.md`](FaasLvFcUsageHigh.md) | Snapshot retention prune lag; check `imaged_prune_lag_seconds`. |
| `build_queue` | page | [`FaasBuildQueueBacklog.md`](FaasBuildQueueBacklog.md) | Builder slot gate tripping on a single customer's burst; rule out by checking the per-account queued count. |
| `wake_latency` | page | [`FaasWakeLatencyHigh.md`](FaasWakeLatencyHigh.md) | Single instance wedged (liveness probe will catch it). |
| `cold_boot` | page | [`FaasColdBootFallbackHigh.md`](FaasColdBootFallbackHigh.md) | FC version upgrade mid-rollout (transient); check `fc_version` label. |
| `cold_boot_ratio` | warn | [`FaasColdBootRatioHigh.md`](FaasColdBootRatioHigh.md) | High churn customer (CI/CD); check per-account breakdown. |
| `api_availability` | page | [`FaasApiAvailabilityLow.md`](FaasApiAvailabilityLow.md) | Scheduled maintenance window; check `#ops-maintenance` Slack. |
| `build_success` | warn | [`FaasBuildSuccessLow.md`](FaasBuildSuccessLow.md) | Failed app dependency update (customer-side); check per-app failure breakdown. |
| `audit_write_failures` | warn | [`FaasApidAuditWriteFailures.md`](FaasApidAuditWriteFailures.md) | Postgres connection storm (transient); check apid error logs. |
| `audit_retention` | warn | [`FaasAuditRetentionExhaustion.md`](FaasAuditRetentionExhaustion.md) | Cleanup-loop goroutine wedged (liveness check). |
| `audit_retention` | page | [`FaasAuditRetentionExhaustion.md`](FaasAuditRetentionExhaustion.md) | None — prune lag >24h is always a real signal. |
| `daemon` | page | [`FaasDaemonDown.md`](FaasDaemonDown.md) | `systemd` unit reload (no restart); check `systemctl status`. |
| `daemon_restart_loop` | warn | [`FaasRestartLoop.md`](FaasRestartLoop.md) | Single mis-deploy (5m transient); wait one cycle before paging. |
| `daemon_repeated_restart` | warn | [`FaasRepeatedRestart.md`](FaasRepeatedRestart.md) | Same as `daemon_restart_loop` but over 1h; persistent signature requires investigation. |
| `daemon_stuck_activating` | warn | [`FaasStuckActivating.md`](FaasStuckActivating.md) | Dependency deadlock (Postgres / etcd / Vault not ready); check `systemctl show` for the failed dependency. |
| `tenant_abuse` | warn | [`tenant-abuse.md`](tenant-abuse.md) | Webhook storm from a single account; check per-account rate. |
| `tenant_gb` | page | [`FaasResidentGbPerCustomerHigh.md`](FaasResidentGbPerCustomerHigh.md) | None — single-tenant GB usage > ceiling is actionable. |
| `traffic_anomaly` | warn | [`FaasTrafficAnomaly.md`](FaasTrafficAnomaly.md) | DDoS (legit customer traffic but unusual shape); check gateway L7 metrics. |
| `githubd` | warn | [`FaasGithubdPathFilterDegraded.md`](FaasGithubdPathFilterDegraded.md) | GitHub App credentials rotation; check `FAAS_GITHUB_APP_*` env. |
| `gateway` | warn | [`GatewayWildcardRoute.md`](GatewayWildcardRoute.md) | Customer-side wildcard route pattern (intentional). |
| `billing` | warn | [`BillingDrift.md`](BillingDrift.md) | Stripe webhook backlog (transient). |
| `domain_doctor` | warn | [`FaasDomainDoctorStalled.md`](FaasDomainDoctorStalled.md) | Single-domain DNS resolution failure (transient). |
| `tls_cert_expiry` | warn | [`FaasTLSCertExpiryWarn.md`](FaasTLSCertExpiryWarn.md) | acme.sh renewal queue lag (transient); check `faas_acme_renewal_lag_seconds`. |
| `tls_cert_expiry` | page | [`FaasTLSCertExpiryPage.md`](FaasTLSCertExpiryPage.md) | None — certs expiring within 24h are always actionable. |
| `tls_on_demand_denied` | warn | [`FaasTLSOnDemandDeniedHigh.md`](FaasTLSOnDemandDeniedHigh.md) | Bot/fuzzer probe traffic; check `gateway_request_origin_asn`. |
| `cpu_starvation` | warn | [`FaasCpuStarvation.md`](FaasCpuStarvation.md) | Tenant burst coinciding with a builder build; check `vmmd_cpu_throttle_ratio{slice}`. |
| `cve_check` | warn | [`FaasNewCve.md`](FaasNewCve.md) | None — every new medium+ CVE is actionable. |
| `failed_login` | warn | [`FaasFailedLoginSpike.md`](FaasFailedLoginSpike.md) | Single bot probing; check `apig_failed_login{ip}` to see if one IP is driving. |
| `restart_loop` | warn | [`FaasRestartLoop.md`](FaasRestartLoop.md) | (See `daemon_restart_loop` above.) |
| `repeated_restart` | warn | [`FaasRepeatedRestart.md`](FaasRepeatedRestart.md) | (See `daemon_repeated_restart` above.) |
| `stuck_activating` | warn | [`FaasStuckActivating.md`](FaasStuckActivating.md) | (See `daemon_stuck_activating` above.) |
| `pg_backup` | page | [`PostgresBackup.md`](../runbooks/PostgresBackup.md) | Network blip on the off-host push channel; check `journalctl -u faas-pg-basebackup-push`. |
| `loki_pipeline` | page/warn | [`FaasLokiPipelineDegraded.md`](../runbooks/FaasLokiPipelineDegraded.md) | Promtail or Loki maintenance can temporarily pause shipping; confirm dropped entries and backend reachability before escalating. |
| `compute_metrics_discovery` | warn | [`FaasComputeMetricsDiscoveryDegraded.md`](../runbooks/FaasComputeMetricsDiscoveryDegraded.md) | A planned compute-node drain can make the registry and target snapshot differ briefly; check the latest discovery timestamp first. |
| `compute_metrics_scrape` | warn | [`FaasComputeMetricsDiscoveryDegraded.md`](../runbooks/FaasComputeMetricsDiscoveryDegraded.md) | A node listener restart or private-route flap can reduce coverage while discovery remains fresh. |
| `log_archive` | page/warn | [`FaasLogArchiveShipperDegraded.md`](../runbooks/FaasLogArchiveShipperDegraded.md) | An intentional object-store maintenance window may create bounded local spool growth; verify capacity and failure reason. |
| `telemetry_pipeline` | info/warn | [`FaasOTLPMetricsExporterDown.md`](../runbooks/FaasOTLPMetricsExporterDown.md) | OTLP export is optional; local Prometheus and trace-ring data remain available when the remote collector is disabled or unavailable. |
| `obs_trace` | info/page | [`FaasOperatorActionTraceCompletenessLow.md`](../runbooks/FaasOperatorActionTraceCompletenessLow.md) | A fresh schedd may not have completed its first driver tick; distinguish cold start from a stalled Postgres-backed loop. |
| `bridge` | page/warn | [`h2c-rollback.md`](h2c-rollback.md) | A deliberate H1 surgical rollback can produce mismatch alerts; confirm the rollback owner and expiry before reverting it. |
| `prometheus_health` | page | [`FaasPrometheusAlertingPathDegraded.md`](../runbooks/FaasPrometheusAlertingPathDegraded.md) | A short Prometheus restart can leave self-scrape series absent; verify service readiness and the active rule groups. |
| `alertmanager_health` | page/warn | [`FaasAlertmanagerDeliveryDegraded.md`](../runbooks/FaasAlertmanagerDeliveryDegraded.md) | Receiver provider outages or a deliberate notification disablement can fail delivery while alert evaluation remains healthy. |

## Cross-cutting triage commands

These commands come up across many alerts; once you know
them, you can skip re-reading the per-alert runbook.

```bash
# Active alerts (sorted by alertname):
amtool alert query --alertmanager.url=$ALERTMANAGER_URL \
  | jq -r '.[] | "\(.labels.alertname) [\(.labels.severity)] \(.startsAt)"' \
  | sort

# Tenant-resident RAM (top 10 by usage):
psql -c "SELECT account_id, SUM(ram_mb + 8) AS mb
         FROM instances WHERE state IN ('waking','cold_booting','running')
         GROUP BY account_id ORDER BY mb DESC LIMIT 10;"

# Compute-node health (last heartbeat per node):
psql -c "SELECT cn.id, cn.active, MAX(cnh.received_at) AS last_hb
         FROM compute_nodes cn
         LEFT JOIN compute_node_heartbeats cnh ON cnh.node_id = cn.id
         GROUP BY cn.id, cn.active ORDER BY last_hb DESC NULLS LAST;"

# Audit-log search for an operator action:
curl -sS -H "Authorization: Bearer $ADMIN_TOKEN" \
  "https://apid.example/v1/admin/obs/audit-log?operator_only=true&since=$(date -u -d '1 hour ago' +%FT%TZ)" \
  | jq '.items[] | {kind, account_id, data, created_at}'
```

## Escalation

If you cannot resolve within the Tier 0 ack window
(15 minutes), follow the
[`escalation.md`](escalation.md) matrix to page Tier 1.

## Pre-flight: known false-positive clusters

These clusters fire often but rarely indicate a real
incident. If you see ONLY these alerts firing in a
window, you can usually defer paging Tier 1:

- `tenant_abuse` single-burst (resolves within 5m).
- `traffic_anomaly` during a deploy (resolves within 10m
  after deploy completion).
- `build_queue` single-customer backlog (the customer
  can usually unblock themselves).
- `daemon_restart_loop` mid-deploy (resolves after the
  deploy finishes replacing the unit).

If MULTIPLE of these fire simultaneously, or if any
fires together with a page-tier alert, treat it as a
real incident.

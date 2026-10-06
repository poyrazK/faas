# Managed PostgreSQL preview degraded

This runbook covers the provider-neutral managed PostgreSQL control-plane
signals emitted by `apid`. The customer provisioning gate is fail-closed by
default; do not open it to work around an alert.

## Signals

The `/metrics` listener exposes these low-cardinality families:

| Metric | Meaning |
| --- | --- |
| `apid_managed_postgres_health_databases{status}` | Whole ready catalog by healthy/degraded/unknown/stale provider metadata. Use max across apid replicas. |
| `apid_managed_postgres_health_checks_total{outcome}` | Accepted healthy/degraded metadata observations. |
| `apid_managed_postgres_health_check_duration_seconds` | Provider observation latency. |
| `apid_managed_postgres_health_sweeps_total{outcome}` | Successful/failed/disabled catalog sweeps. |
| `apid_managed_postgres_health_last_sweep_timestamp_seconds` | Last completed sweep, including recorded provider failures. |
| `apid_managed_postgres_health_enabled` | Independent read-only health policy. |
| `apid_managed_postgres_health_stale_after_seconds` | Configured observation freshness window. |
| `apid_managed_postgres_reconcile_total{resource,operation,outcome}` | Database or binding lifecycle attempts. |
| `apid_managed_postgres_reconcile_duration_seconds{resource,operation}` | Lifecycle attempt latency. |
| `apid_managed_postgres_usage_collection_sweeps_total{outcome}` | Complete, degraded, failed, or disabled usage sweeps. |
| `apid_managed_postgres_usage_last_success_timestamp_seconds` | Freshness of the latest complete usage sweep. |
| `apid_managed_postgres_usage_collection_databases{state}` | Counts from the latest usage sweep, including `state="included_in_source"` for restore descendants covered by the source aggregate. |
| `apid_managed_postgres_usage_policy_enabled` | Whether usage guardrails are enabled. |
| `apid_managed_postgres_provisioning_enabled` | Current evaluated staging rollout gate. |
| `apid_managed_postgres_canary_admission_total{outcome}` | Canary allow/deny decisions. |
| `apid_managed_postgres_admission_denied_total{reason}` | Stable COGS/admission denial reasons. |

No metric label contains an account ID, app ID, provider resource ID,
credential, connection URL, or provider error text.

## Triage

1. Check the alert's `component=apid` series and the latest structured apid
   log line. Keep `FAAS_MANAGED_POSTGRES_QUALIFIED` and
   `provisioning_enabled` unchanged while investigating.
2. For reconciliation failures, inspect the resource state and retry code.
   Verify the configured backend fingerprint still matches the persisted
   database placement. Do not issue a second provider create manually;
   recovery discovers the accepted opaque resource identity.
3. For deferred work, distinguish a closed rollout gate from provider or
   parent-resource readiness. Deletion and credential revocation must continue
   even when provisioning is disabled.
4. For stale usage, confirm the collector can reach the provider and that the
   complete provider window has closed. New reservations intentionally fail
   closed until `usage_last_success_timestamp_seconds` advances.
5. If the provider is unavailable, leave provisioning disabled and drain
   known resources through the normal reconciler. Never delete catalog rows as
   a shortcut; account deletion requires provider-confirmed tombstones.

## Recovery validation

After the underlying issue is fixed, verify that:

- a complete usage sweep records `outcome="success"` and advances the last
  success timestamp;
- reconciliation failures stop increasing for at least one full retry window;
- the canary allowlist still contains only the intended staging accounts; and
- a disposable staging database can complete create → ready → bind → delete,
  with the provider qualification artifact still unexpired.

If any check is missing, keep the rollout gate closed and attach the metrics
snapshot plus the qualification artifact to the incident.

## Provider health alerts

`FaasManagedPostgresHealthDegraded` means a recent attempt found an API failure,
a missing resource, spec drift, an invalid observation, or unavailable backend.
Read `gregale postgres get <database>` for the stable health code and timestamps.
`backend_unavailable` requires comparing the configured backend fingerprint
with persisted placement; do not repoint a catalog row to another organization.
`resource_missing` requires checking provider metadata and ownership before any
operator recovery. Monitoring never automatically recreates missing resources.

`FaasManagedPostgresHealthStale` means checks aged beyond the configured window
or an old ready database has never been checked. Compare request backoff,
provider latency, catalog size, and the collector's bounded sweep capacity.
Freshness tracks attempts, so a fresh degraded result can be an ongoing outage.
The success timestamp tracks valid metadata responses, including degraded ones.

`FaasManagedPostgresHealthCollectorStalled` means an enabled process has not
completed a sweep within its freshness window, with an additional five-minute
alert hold. Check apid logs, migration application, and catalog connectivity.
Provider failures alone should advance the heartbeat with degraded checks;
store failures prevent completion. Check every enabled replica, even with an
empty catalog. An explicitly disabled collector does not alert.

After recovery, verify the sweep timestamp advances, stale counts fall, and
health reaches healthy for the intended resources. Suspended compute is healthy
and monitoring must leave it suspended. Use the existing application connection
probe when SQL reachability is needed; provider metadata does not prove it.
Keep the provisioning gate closed until the separate staging qualification is
valid. Disable `health.enabled` and restart apid to pause monitoring during a
rollback; no provider resources need to be mutated.

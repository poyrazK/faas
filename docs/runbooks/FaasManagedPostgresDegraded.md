# Managed PostgreSQL preview degraded

This runbook covers the provider-neutral managed PostgreSQL control-plane
signals emitted by `apid`. The customer provisioning gate is fail-closed by
default; do not open it to work around an alert.

## Signals

The `/metrics` listener exposes these low-cardinality families:

| Metric | Meaning |
| --- | --- |
| `apid_managed_postgres_reconcile_total{resource,operation,outcome}` | Database or binding lifecycle attempts. |
| `apid_managed_postgres_reconcile_duration_seconds{resource,operation}` | Lifecycle attempt latency. |
| `apid_managed_postgres_usage_collection_sweeps_total{outcome}` | Complete, degraded, failed, or disabled usage sweeps. |
| `apid_managed_postgres_usage_last_success_timestamp_seconds` | Freshness of the latest complete usage sweep. |
| `apid_managed_postgres_usage_collection_databases{state}` | Counts from the latest usage sweep. |
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


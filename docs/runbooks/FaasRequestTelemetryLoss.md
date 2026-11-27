# FaasRequestTelemetryLoss

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `gateway_request_telemetry_platform_dropped_total`,
`gateway_request_telemetry_overwritten_total` (gatewayd-internal, per node),
`apid_request_telemetry_recorded_total{outcome=~"db_error|invalid"}` (apid).
Severity: warn. Requests are served; only their telemetry is lost.

## Symptom

Request telemetry rows were lost in the last 15 minutes. The affected apps'
analytics windows read `telemetry_window_incomplete`, and the canary circuit
breaker and the 5xx rollback hold over windows that lost rows (they judge the
covered tail after a gateway restart, not a window with losses). Billing is
not affected: it reads the usage outbox, not request telemetry.

## Diagnose

1. Which side lost them:
   - `sum by (instance) (increase(gateway_request_telemetry_platform_dropped_total[15m]))`:
     the gateway publisher gave up on rows (retries exhausted with apid
     unreachable, slow, or answering errors; a full backlog; the shutdown
     deadline). `gateway_request_telemetry_dropped_total` also counts rows apid
     refused for their app (a customer's ingest quota), which do not alert. Check the node's
     `faas-gatewayd-internal` journal for `request telemetry` warnings.
   - `increase(gateway_request_telemetry_overwritten_total[15m])`: the ring was
     full before the publisher drained it; the publisher is behind or apid is
     slow. Look for the same warnings and for apid latency.
   - `sum by (outcome) (increase(apid_request_telemetry_recorded_total[15m]))`:
     `db_error` is an insert failure (check apid logs and Postgres);
     `invalid` is a row apid will never accept, logged at most once a minute as
     `request telemetry row refused` with `outcome=invalid` and its `reason` (`app_or_account_gone`
     right after an app or account deletion is expected and small).
2. Coverage per node: `SELECT node_name, healthy_since, received_at,
   unattributed_dropped_total, clean_stop_at FROM request_telemetry_coverage;`
   A `healthy_since` that keeps moving means the node keeps losing rows or
   restarting uncleanly.

## Resolve

Fix the cause (apid or Postgres health, a gateway restart loop). The loss
itself cannot be recovered; the gap ages out of analytics with the coverage
retention.

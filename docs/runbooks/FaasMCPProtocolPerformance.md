# MCP protocol performance alerts

## Symptom

These signals aggregate starter-reported RPC events attributed by the host to an
app UUID. They are diagnostic evidence, not a complete host-verified request ledger.
Alerts indicate elevated RPC failures, slow execution, aggregation capacity
pressure, or missing telemetry heartbeats.

Alerts are internal operator warnings through existing Alertmanager routing.
They do not automatically roll back, promote, or otherwise mutate deployments.

## Check

For `FaasMCPProtocolHighErrorRate`, resolve the alert's app UUID and open its MCP
dashboard filter. Compare failures and cancellations, then inspect structured
`mcp_protocol_request` logs. RPC errors, tool errors, authorization errors, internal
errors and timeouts count as failures. Cancellations do not. Check provider and
downstream dependency failures before changing capacity or application code.

For `FaasMCPProtocolSlowExecution`, compare execution p50/p95/p99 with stream p95.
A long SSE stream alone does not fire this alert. Subscription acknowledgement
measures setup time; background task execution is excluded. Histogram estimates
require at least 20 observations in every evaluated five-minute window. The rule
requires p95 above five seconds for ten minutes.

No recent events can mean idle traffic, missing instrumentation, dropped output,
or an unavailable host. Check vmmd scrape health and the starter version. Missing
charts are not evidence of zero calls. Counters are approximate across scrapes and
reset on vmmd restart or aggregate expiry. The original logs remain the place to
inspect individual observations.

## Recover

Restore failing providers or downstream dependencies, or deploy a reviewed fix
for the application error identified in its structured logs. For aggregation
pressure, adjust placement or the collector's cardinality budget after reviewing
host capacity. Confirm error rates, execution latency and telemetry coverage have
returned to normal before closing the incident.

For `FaasMCPProtocolAggregationCapacity`, a vmmd collector has filled its bounded
1,024-app table. Apps expire after one hour without accepted events. Check active
app placement and host load, and consider increasing capacity with an explicit
cardinality budget or moving workloads. Do not restart a healthy VM to clear the
warning: restarting the collector loses its in-memory counters and does not fix
capacity pressure. Charts may omit dropped observations while this alert fires.

## Telemetry heartbeat loss

`FaasMCPTelemetryHeartbeatLost` is an opt-in operator alert, enabled by setting
`MCP_TELEMETRY_LOSS_ALERT=true` on an updated starter. Starters emit immediately
and every 30 seconds. Host intake records the timestamp and supplies app,
deployment and instance identity. Two minutes without a heartbeat is stale;
three minutes of startup/resume grace and five sustained minutes are required
before notification. A heartbeat is independent of RPC completions and does not
prove readiness, authentication correctness or tool success.

Check the selected application's MCP dashboard coverage against scheduler
inventory. Missing host inventory/scrapes are unavailable, not healthy. Inspect
process logs, host log intake and Prometheus scraping. Paused/frozen/warm guests
are excluded by host pause callbacks; resume resets the grace period. Parking
and instance teardown remove the series. This alert cannot detect a missing
vmmd scrape or a starter that never emitted its first opt-in heartbeat; use
existing host/exporter availability monitoring for that case. It routes through
operator Alertmanager and does not create a customer webhook rule.

`vmmd_mcp_telemetry_capacity_dropped_total` counts coverage omissions when the
4096-instance per-host bound is reached. Coverage remains incomplete rather than
claiming full reporting. Protocol aggregation retains its separate 1024-app bound.

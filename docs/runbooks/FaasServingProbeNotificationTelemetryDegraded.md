# FaasServingProbeNotificationTelemetryDegraded

An expected compute node has a fresh, unique probe source, but required direct
webhook or heartbeat telemetry has remained missing or invalid for five
minutes. The warning uses the normal `warn` route, family
`serving_probe_notifications`, component `node`. It preserves only the stable
node and fixed channel plus routing metadata.

Its value is the evidence code: **2** means required fields are missing;
**3** means fields are malformed, duplicated, stale relative to the source
scrape, or inconsistent. This is lost evidence. It does not establish a failed
submission, provider outage, serving outage or external receipt.

## Inspect the evidence

```bash
gregalectl obs node fsn-2
gregalectl obs node fsn-2 --json
```

The `probe_notifications` section returns both expected channels directly,
including nodes omitted from the fleet preview. Read `reason`, optional
`evidence_reason`, `evaluated_at`
and the independent inventory status. Unknown outcomes preserve null history.
Older rules may provide only `recording_unknown`; unavailable or mismatched
recordings cannot establish a current detailed cause.

Inspect the underlying recordings when needed:

```promql
faas_serving_probe_notification:state{probe_node="fsn-2"}
faas_serving_probe_notification:evidence_status{probe_node="fsn-2"}
```

Coarse state 1 is unknown. Evidence status 0 describes complete outcome evidence
or explicit disablement; 1 describes a valid enabled channel without history;
2 and 3 identify telemetry faults; 4 means the probe source is unavailable.
Codes 0, 1 and 4 do not trigger this warning. Inventory and probe-source failures
have separate serving coverage warnings.

## Fix the telemetry

On the node, inspect the service and metrics writer using safe diagnostics:

```bash
systemctl status faas-serving-probe.timer faas-serving-probe.service
journalctl -u faas-serving-probe.service -n 50 --no-pager
```

Check helper/rule convergence, successful textfile writes and one discovered
node-exporter source. Required `enabled` and `attempted` fields must be unique,
boolean and from the same scrape as the run clock. An attempted request or any
retained history requires an outcome, failure code and completion clock. A
success also requires its matching success clock; a failed submission can have
no historical success. Do not copy raw provider labels, destination credentials
or response text into issues.

## Confirm the result

Fresh complete telemetry should return code 0 with the completed outcome, or
code 1 for a valid quiet channel without history. A known failed submission
returns to the separate failure warning after its own hold. Explicit disablement
also returns code 0; credential rotation can return code 1. Neither proves a
successful submission. Source or inventory loss can also clear this warning;
inspect their status before treating resolution as repaired telemetry.

Use the [serving-health guide](../ops/serving-health.md#inspect-direct-notification-submissions)
for clock semantics and keep the independent external missing-heartbeat check
for control-plane and shared-provider failures.

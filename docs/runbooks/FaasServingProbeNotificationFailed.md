# FaasServingProbeNotificationFailed

The compute-node serving probe's last completed `webhook` or `heartbeat`
submission failed, and a fresh, unique source has continued reporting that
outcome for five minutes. This warning uses Alertmanager's normal `warn` route
(family `serving_probe_notifications`, component `node`).

The alert identifies a stable `probe_node` and a fixed channel. Its value is
the failure code: 1 DNS, 2 connection, 3 TLS, 4 total timeout, 5 non-2xx HTTP
status (including a rejected redirect), or 8 an unclassified failure, including
an oversized or incomplete response. Destination URLs, provider response text,
scrape addresses and credentials are excluded from the alert.

This is the last completed submission outcome. It does not establish a current
provider outage, human receipt or a serving outage. Serving can pass while a
heartbeat submission fails. An intentionally skipped run preserves the last
completed outcome; it does not confirm recovery.

## Inspect the evidence

Use the alert's node name for a direct lookup, including nodes omitted from the
fleet serving-health preview:

```bash
gregalectl obs node fsn-2
gregalectl obs node fsn-2 --json
```

The `probe_notifications` section contains both channels when fresh monitoring
intent expects this node. Unknown inventory leaves `expected=null`; a node
outside fresh configured intent has `expected=false` and no channel results.
Neither case establishes recovery. See the
[node lookup guide](../ops/serving-health.md#inspect-one-node-beyond-the-fleet-preview)
for freshness and compatibility behavior.

For the underlying evidence, inspect these recordings:

```promql
faas_serving_probe_notification:state{probe_node="fsn-2"}
faas_serving_probe_notification:last_attempt_failure_code{probe_node="fsn-2"}
faas_serving_probe_notification:last_attempt_timestamp_seconds{probe_node="fsn-2"}
faas_serving_probe_notification:last_success_timestamp_seconds{probe_node="fsn-2"}
```

State 0 means disabled, 1 means unknown, 2 means the last completed submission
succeeded, and 3 means it failed. Read companion clocks with the state: partial
metadata cannot establish a complete outcome. A historical successful
submission is not proof that another request would succeed now.

The rules require current fleet intent, one discovered source for the node,
a successful scrape no older than 120 seconds, a probe run within that node's
configured result-age policy, and matching scrape timestamps for every used
field. Duplicate, future, stale, missing or contradictory evidence becomes
unknown. The source run clock cannot be refreshed by later provider latency.

On that node, read the service's fixed diagnostics:

```bash
systemctl status faas-serving-probe.timer faas-serving-probe.service
journalctl -u faas-serving-probe.service -n 50 --no-pager
```

Look for `notification_submission_failed` or `heartbeat_submission_failed`
and its `failure_code`. Do not print credential files or copy destination URLs
into issues. Each submission has a ten-second total deadline and a 4096-byte
response limit; redirects and non-2xx responses fail.

## Fix the failure

For DNS/connection errors, check node DNS and outbound reachability. For TLS,
check trust and the node clock. For HTTP failures, verify the provisioned
destination and provider-side availability or throttling. For unknown failures,
check for invalid HTTPS credentials, oversized/incomplete responses, or a
failed request worker, using the service's safe diagnostics.

## Confirm recovery

A later completed 2xx submission with a complete bounded response changes state
to 2 and advances the success clock. Webhook incident messages that failed
submission remain due on the next run. Heartbeats run only when serving passes;
quiet webhooks and suppressed heartbeats do not manufacture recovery evidence.

Credential rotation clears the channel's history. Unknown evidence and disabling
a channel also stop this particular warning. An Alertmanager resolution
therefore needs the current state and clocks to distinguish a successful
submission from lost evidence or disablement. Use the existing serving coverage
and inventory warnings for stale or unavailable probes. An older helper without
notification metrics remains unknown and needs convergence before this evidence
can be assessed.

For a fresh probe source, missing or invalid notification fields trigger
[`FaasServingProbeNotificationTelemetryDegraded`](FaasServingProbeNotificationTelemetryDegraded.md)
after five minutes. The node lookup's optional `evidence_reason` distinguishes
`telemetry_missing` and `telemetry_invalid` from a valid `no_submission_yet`
channel, including after credential rotation. Source outages retain
`source_unavailable`; they do not
become evidence of notification recovery.

These alerts depend on the control-plane monitoring stack. If Alertmanager
shares the failed destination/provider, its warning may also fail to arrive.
Keep the independent external missing-heartbeat check. See the
[transport guide](../ops/serving-probe.md#direct-notification-transport-evidence)
and [notification qualification](../ops/monitoring-qualification.md) for the
separate operational checks.

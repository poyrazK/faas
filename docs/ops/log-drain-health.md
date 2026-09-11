# Customer log-drain health

Customer runtime log drains are delivered by `gatewayd-internal` through a
bounded, non-blocking queue. The delivery path is best effort: a slow or
unreachable destination must not block application requests, but records can
be dropped when the queue is full, the source ring reports a gap, or the
worker is shutting down.

## Metrics

All series use the bounded `{app, kind}` labels. `kind` is `http_json` or
`otlp`.

| Metric | Meaning |
| --- | --- |
| `gateway_log_drain_active` | The drain worker is running. |
| `gateway_log_drain_queue_depth` / `gateway_log_drain_queue_capacity` | Current queue usage and its configured bound. |
| `gateway_log_drain_delivery_latency_seconds` | Enqueue-to-success latency, including retries. |
| `gateway_log_drain_delivered_total` | Records accepted with a 2xx response. |
| `gateway_log_drain_failed_total` | Records that exhausted delivery attempts. |
| `gateway_log_drain_dropped_total` | Records rejected by a full or stopped queue. |
| `gateway_log_drain_retries_total` | Individual retry attempts. |
| `gateway_log_drain_last_success_timestamp_seconds` | Unix time of the last successful response. |
| `gateway_log_drain_last_failure_timestamp_seconds` | Unix time of the last terminal delivery failure. |
| `gateway_log_drain_stream_reconnects_total` | Source-stream restarts after an established stream ended. |
| `gateway_log_drain_gaps_total` | Source ring gaps observed before delivery. |

Useful PromQL checks:

```promql
# Queue saturation by drain
gateway_log_drain_queue_depth
  / gateway_log_drain_queue_capacity > 0.8

# Destinations failing without a recent success
increase(gateway_log_drain_failed_total[5m]) > 0
and on (app, kind)
time() - gateway_log_drain_last_success_timestamp_seconds > 300

# Source-side log loss, independent of endpoint health
increase(gateway_log_drain_gaps_total[5m]) > 0
or increase(gateway_log_drain_dropped_total[5m]) > 0
```

## Triage

1. Check `gateway_log_drain_active`. If it is `0`, inspect the gatewayd
   reconcile log and the drain configuration in the app API.
2. Compare queue depth with capacity and inspect delivery latency. A full
   queue indicates destination backpressure; `http_json` and `otlp` endpoints
   are both expected to return promptly with a 2xx response.
3. Check `failed_total`, `retries_total`, and the endpoint's response status in
   gatewayd logs. Credentials are never logged.
4. Check `gaps_total` separately. A gap means the per-instance runtime ring
   no longer retained the missing lines; repairing the destination will not
   recover them.
5. Use the app log-drain API to confirm the configured destination and rotate
   its credential if needed. Do not expose the sealed credential in support
   output.

## Customer health surface

`gatewayd-internal` batches the current snapshot every 15 seconds into the
`app_log_drain_health` table. The snapshot survives a gateway restart and is
available through:

```text
GET /v1/apps/{slug}/log-drains/{id}/health
```

The response includes queue depth/capacity, delivery/failure/drop/retry
counters, stream reconnects, source gaps, last success/failure timestamps,
and a short sanitized error summary. It never includes the configured auth
header or a raw transport error. The same state is shown at
`/dashboard/apps/{slug}/log-drains`.

The durable view is a health snapshot, not a replay ledger: a restart or a
source-ring gap cannot recover records that were already lost. Prometheus
remains the source for historical time series and alert evaluation, while the
API/dashboard provide the current per-drain state.

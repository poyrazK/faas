# Customer log-drain health

Customer runtime log drains are delivered by `gatewayd-internal` through a
per-drain durable local outbox. The stream producer writes a record to disk
before acknowledging it, and the delivery worker advances the cursor only
after a 2xx response. A slow or unreachable destination therefore creates a
bounded backlog instead of losing records on a worker restart. Records can
still be dropped when the outbox is full, the source ring reports a gap, or a
record is moved to the bounded dead-letter file after retry exhaustion.

The outbox defaults to `/var/lib/faas/log-drains`. It can be relocated with
`FAAS_LOG_DRAIN_SPOOL_ROOT` when the service's systemd `ReadWritePaths`
allowlist is updated to include the replacement directory. Each drain has a
64 MiB active-record budget and an 8 MiB newest-entry dead-letter budget.

## Metrics

All series use the bounded `{app, kind}` labels. `kind` is `http_json` or
`otlp`.

| Metric | Meaning |
| --- | --- |
| `gateway_log_drain_active` | The drain worker is running. |
| `gateway_log_drain_queue_depth` / `gateway_log_drain_queue_capacity` | Legacy in-memory sender queue gauges; production alerting uses the durable outbox gauges below. |
| `gateway_log_drain_pending_records` | Records waiting in the durable outbox. |
| `gateway_log_drain_pending_bytes` / `gateway_log_drain_pending_bytes_capacity` | Durable outbox byte backlog and capacity. |
| `gateway_log_drain_dead_letter_total` | Records retained after retry exhaustion. |
| `gateway_log_drain_oldest_pending_timestamp_seconds` | Unix time of the oldest durable record still awaiting delivery, or `0` when empty. |
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
# Durable outbox saturation by drain
gateway_log_drain_pending_bytes_capacity > 0
and gateway_log_drain_pending_bytes
  / gateway_log_drain_pending_bytes_capacity > 0.8

# Destinations failing without a recent success
increase(gateway_log_drain_failed_total[5m]) > 0
and on (app, kind)
time() - gateway_log_drain_last_success_timestamp_seconds > 300

# Source-side log loss, independent of endpoint health
increase(gateway_log_drain_gaps_total[5m]) > 0
or increase(gateway_log_drain_dropped_total[5m]) > 0

# Durable backlog older than five minutes
gateway_log_drain_oldest_pending_timestamp_seconds > 0
and time() - gateway_log_drain_oldest_pending_timestamp_seconds > 300

# Dead letters require intervention
gateway_log_drain_dead_letter_total > 0
```

## Triage

1. Check `gateway_log_drain_active`. If it is `0`, inspect the gatewayd
   reconcile log and the drain configuration in the app API.
2. Compare durable pending bytes with capacity and inspect delivery latency. A
   full outbox indicates destination backpressure; `http_json` and `otlp`
   endpoints are both expected to return promptly with a 2xx response.
3. Check the durable `pending_records` / `pending_bytes` values and the
   `oldest_pending_at` timestamp. A growing backlog indicates endpoint
   backpressure; `dead_letter_total` means records need operator replay or
   customer endpoint repair before they can be recovered.
4. Check `failed_total`, `retries_total`, and the endpoint's response status in
   gatewayd logs. Credentials are never logged.
5. Check `gaps_total` separately. A gap means the per-instance runtime ring
   no longer retained the missing lines; repairing the destination will not
   recover them.
6. Use the app log-drain API to confirm the configured destination and rotate
   its credential if needed. Do not expose the sealed credential in support
   output.

## Customer health surface

`gatewayd-internal` batches the current snapshot every 15 seconds into the
`app_log_drain_health` table. The snapshot survives a gateway restart and is
available through:

```text
GET /v1/apps/{slug}/log-drains/{id}/health
```

The response includes the durable pending record/byte counts, outbox capacity,
oldest pending timestamp, dead-letter count, delivery/failure/drop/retry
counters, stream reconnects, source gaps, last success/failure timestamps,
and a short sanitized error summary. It never includes the configured auth
header or a raw transport error. The same state is shown at
`/dashboard/apps/{slug}/log-drains`.

The durable view is still a health snapshot, not a customer replay API. A
source-ring gap cannot recover records that were already lost, and dead-letter
records currently require node-level operator handling. Prometheus remains the
source for historical time series and alert evaluation, while the API/dashboard
provide current per-drain state.

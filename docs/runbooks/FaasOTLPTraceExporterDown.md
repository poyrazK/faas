# FaasOTLPTraceExporterDown

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.

## Meaning

`FaasOTLPTraceExporterDown` means a daemon has
`*_otel_trace_exporter_enabled == 1` and its most recent OTLP batch export
failed. The local daemon remains available, but the remote trace backend is
not receiving new spans; failed batches are counted as potentially dropped.

The signal covers both the shared `pkg/wire/otelinit` pipeline and the
gatewayd-public trace-ring pipeline. A daemon without an OTLP endpoint
publishes the health metrics with `enabled == 0` and does not alert.

## Verify

```bash
# Which targets are configured but down?
curl -fsS --data-urlencode \
  'query=({__name__=~".*_otel_trace_exporter_enabled"} == 1) and on (job, instance) ({__name__=~".*_otel_trace_exporter_up"} == 0)' \
  'http://127.0.0.1:9095/api/v1/query'

# Recent failures and potentially dropped spans
curl -fsS --data-urlencode \
  'query=sum by (job, instance) (rate({__name__=~".*_otel_trace_export_total", trigger="batch", outcome="error"}[10m]))' \
  'http://127.0.0.1:9095/api/v1/query'
curl -fsS --data-urlencode \
  'query=sum by (job, instance) (rate({__name__=~".*_otel_trace_spans_dropped_total"}[10m]))' \
  'http://127.0.0.1:9095/api/v1/query'

# Export latency and last successful delivery
curl -fsS --data-urlencode \
  'query=histogram_quantile(0.99, sum by (job, instance, le) (rate({__name__=~".*_otel_trace_export_duration_seconds_bucket"}[10m])))' \
  'http://127.0.0.1:9095/api/v1/query'
curl -fsS --data-urlencode \
  'query=time() - max by (job, instance) ({__name__=~".*_otel_trace_last_success_timestamp_seconds"})' \
  'http://127.0.0.1:9095/api/v1/query'
```

Check the affected daemon's logs for `otelinit: wired OTLP/HTTP exporter` or
`trace_setup: OTLP exporter enabled`, then inspect the configured
`OTEL_EXPORTER_OTLP_ENDPOINT`. Explicit `https://` endpoints require the
collector's certificate chain; explicit `http://` and bare host:port values
are plaintext compatibility modes.

## Recover

1. Confirm the collector is listening and accepting OTLP/HTTP traces on the
   configured endpoint.
2. Fix DNS, routing, firewall, credentials, or TLS configuration as needed.
3. Restart the affected daemon only when the endpoint configuration changed
   or the exporter cannot recover on its own. Do not restart a serving daemon
   solely because the remote exporter is down.
4. Verify `*_otel_trace_exporter_up == 1`, a rising
   `*_otel_trace_last_success_timestamp_seconds`, and zero new errors for at
   least one batch interval.

`FaasOTLPTraceExporterErrors` is info-tier and may fire for intermittent
failures even when the exporter has recovered. Treat the page/warn alert as a
remote observability incident, not as proof of a customer-serving outage.


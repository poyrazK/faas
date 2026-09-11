# Trace exemplars for latency investigation

The gateway attaches a sampled request's `trace_id` as a Prometheus exemplar
to these histograms:

- `gateway_request_duration_seconds`
- `gateway_wake_latency_seconds`
- `gateway_wake_latency_seconds_by_node`

The trace ID is exemplar metadata, not a Prometheus label. It therefore does
not create one time series per request. Unsampled requests and tracing-disabled
daemons continue to emit ordinary histogram observations without an exemplar.

## Grafana path

The fleet dashboard contains request- and wake-latency heatmaps configured to
render exemplar markers. Grafana's Prometheus datasource must have an exemplar
destination configured for a click-through link:

- `gv_trace_datasource_uid` when a Tempo, Jaeger, Datadog, or other supported
  trace datasource is already provisioned in Grafana; or
- `gv_trace_exemplar_url` for a direct viewer URL containing Grafana's escaped
  `$${__value.raw}` macro.

The destination is intentionally optional. This repository does not require a
specific trace backend, so the heatmaps still expose the raw `trace_id` when
the provider has not selected one.

## Observer endpoint fallback

When `FAAS_TRACE_OBSERVER_TOKEN` is configured, fetch the in-memory trace ring
using the trace ID from the exemplar:

```sh
curl -sS \
  -H "X-Faas-Trace-Auth: $FAAS_TRACE_OBSERVER_TOKEN" \
  "http://127.0.0.1:8080/v1/traces/$TRACE_ID"
```

Use the gateway's actual observer address when it differs from the example.
The endpoint is disabled (404) when the observer token is empty. A trace can
also be absent after the ring's rolling retention window or when the request's
span was not sampled.

## Verification

1. Confirm the Prometheus target exposes the histogram buckets and that the
   Prometheus process includes `--enable-feature=exemplar-storage`.
2. Open the request or wake heatmap for a range containing traffic.
3. Hover an exemplar marker and verify its label is `trace_id` with 32
   lowercase hexadecimal characters.
4. Follow the configured destination, or use the observer endpoint fallback.
5. If exemplars are absent but counts move, check the daemon's
   `*_otel_trace_exporter_enabled` / `*_otel_trace_exporter_up` metrics and
   the `FaasOTLPTraceExporterDown` runbook. Exporter health is best-effort and
   does not stop local Prometheus observations.

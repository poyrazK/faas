# FaasOTLPMetricsExporterDown

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.

Metrics:

- `<daemon>_otel_metrics_exporter_enabled` — `1` when the daemon has a
  valid OTLP metrics endpoint configured, otherwise `0`.
- `<daemon>_otel_metrics_exporter_up` — `1` when the most recent export
  succeeded, otherwise `0`.
- `<daemon>_otel_metrics_export_total{trigger,outcome}` — export attempts,
  with `trigger` in `{periodic, shutdown}` and `outcome` in `{success, error}`.
- `<daemon>_otel_metrics_last_success_timestamp_seconds` — Unix timestamp of
  the latest successful export.
- `<daemon>_otel_metrics_export_duration_seconds` — export duration histogram.

## Meaning

`FaasOTLPMetricsExporterDown` means a daemon is configured to push its local
Prometheus registry to an OTLP collector, but the latest push has failed for
10 minutes. The daemon remains available and its local `/metrics` endpoint is
still the source of truth; only the remote metrics backend is stale.

An intentionally disabled bridge has `exporter_enabled == 0` and does not
fire this alert.

## Triage

1. Identify the affected scrape target and inspect:

   ```promql
   <daemon>_otel_metrics_exporter_up
   rate(<daemon>_otel_metrics_export_total{outcome="error"}[10m])
   <daemon>_otel_metrics_last_success_timestamp_seconds
   histogram_quantile(0.95, rate(<daemon>_otel_metrics_export_duration_seconds_bucket[10m]))
   ```

2. Check the daemon logs for `otlp metrics export failed` and confirm the
   configured `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` (or fallback
   `OTEL_EXPORTER_OTLP_ENDPOINT`) resolves from that host.

3. Check collector health and network reachability. A 4xx/5xx response usually
   indicates collector routing or authentication; a timeout or connection
   error usually indicates network, DNS, or collector availability.

4. If the collector is intentionally unavailable, keep local Prometheus
   scraping active and restore the collector before relying on remote
   dashboards. The bridge retries on its next interval and performs a bounded
   final attempt during graceful shutdown.

## Related signals

- `FaasOTLPMetricsExporterErrors` records recovered or intermittent failures.
- Daemon readiness and customer-facing SLO alerts are independent; do not
  restart a serving daemon solely because this remote exporter is down.

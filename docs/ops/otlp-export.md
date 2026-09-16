# OTLP export

Gregale keeps local Prometheus/Grafana and the gateway trace ring as the
availability-preserving observability path. Optional OpenTelemetry Protocol
(OTLP/HTTP) export sends daemon traces and metrics to an operator-selected
collector; a collector outage must not stop a serving daemon.

## Enable export

The Ansible `otlp_exporter` role installs the example at
`/etc/faas/otel.env.example`. Release-managed service units load the
operator-owned `/etc/faas/otel.env` directly when it exists. Ansible never
writes the real file because it can contain credentials.

On each FaaS host, create `/etc/faas/otel.env` as `root:root` mode `0400`:

```dotenv
OTEL_EXPORTER_OTLP_ENDPOINT=https://otel-collector.example:4318
OTEL_RESOURCE_ATTRIBUTES=deployment.environment=production,faas.node.name=fsn-1
OTEL_TRACES_SAMPLER_ARG=0.10
OTEL_METRIC_EXPORT_INTERVAL=60000
```

Use `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` when metrics need a different
collector route, or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` when traces need a
different route. The generic endpoint is used for both signals otherwise.
Endpoints without a scheme and explicit `http://` endpoints are plaintext;
production cross-host export should use `https://`.

Headers and private PKI use the standard OTLP variables:

```dotenv
OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer%20REPLACE_ME
OTEL_EXPORTER_OTLP_CERTIFICATE=/etc/faas/secrets/otel/ca.crt
OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE=/etc/faas/secrets/otel/client.crt
OTEL_EXPORTER_OTLP_CLIENT_KEY=/etc/faas/secrets/otel/client.key
```

The trace exporter consumes the standard OTLP HTTP configuration directly. The
Prometheus-to-OTLP metrics bridge supports generic and metrics-specific
headers, CA bundles, mTLS client certificates, and timeout values. Certificate
and key files are host-local secret material; keep them outside Git and use
the existing secret rotation process.

After changing the file, reload and restart the instrumented services present
on that host. On a control-plane host:

```sh
sudo systemctl daemon-reload
sudo systemctl restart faas-apid faas-schedd faas-meterd \
  faas-gatewayd-public faas-githubd faas-outboundd faas-s3-gatewayd
```

On a compute-only host:

```sh
sudo systemctl restart faas-vmmd faas-gatewayd-internal \
  faas-builderd faas-imaged faas-realtimed
```

## Collector routing

Keep the Gregale daemons vendor-neutral. Configure the collector to fan out
OTLP traces and metrics to the desired backend, for example Datadog or Tempo.
This keeps provider API keys and backend-specific retry/transform policy in the
collector rather than in every Gregale service.

The daemon service identity is `service.name` (`apid`, `schedd`,
`gatewayd-public`, `realtimed`, `outboundd`, `s3-gatewayd`, and so on) and
`service.version` is the running Gregale version. Add deployment or host
identity through `OTEL_RESOURCE_ATTRIBUTES`; do not put account IDs, request
paths, or other unbounded values there.

## Verify

Check the local Prometheus endpoint for the exporter health metrics:

```sh
curl -fsS http://127.0.0.1:<daemon-metrics-port>/metrics \
  | grep -E 'otel_(trace|metrics)_exporter_(enabled|up)|otel_(trace|metrics)_last_success'
```

For each configured daemon, `*_otel_trace_exporter_enabled` and
`*_otel_metrics_exporter_enabled` should be `1`, and the corresponding `up`
gauge should remain `1` after a batch is sent. The Grafana
`telemetry-pipeline.json` dashboard and the `FaasOTLPTraceExporterDown`,
`FaasOTLPMetricsExporterDown`, and error alerts show failures without masking
the local Prometheus path.

If export is intentionally disabled, omit `/etc/faas/otel.env`; the health
gauges remain `0` and local dashboards continue to work.

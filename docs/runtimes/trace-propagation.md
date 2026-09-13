# Trace propagation — request-scoped W3C context

Gregale stamps every incoming request with a W3C
[`traceparent`](https://www.w3.org/TR/trace-context/) header and
forwards the request-scoped context to your function (issue #555
layer 4). You can opt into OpenTelemetry auto-instrumentation and
the platform's trace will join your spans, all the way to the OTLP
collector you point at
`OTEL_EXPORTER_OTLP_ENDPOINT`.

This page is the operator's quick-start; the spec contract is in
`docs/faas_implementation_spec.md` §16 (tracing).

## What the platform gives you

- **Header name (HTTP)**: `traceparent` — the standard W3C name.
  The Gregale edge gateway already accepts and forwards it.
- **Header metadata**: `tracestate` is forwarded when valid and within
  the 512-byte W3C limit. `baggage` is forwarded per request when it is
  within Gregale's 2 KiB / 16-member guest-boundary budget.
- **Env var (runner)**: `TRACEPARENT` — a boot/wake seed for runtimes
  that initialize tracing before serving requests. It is not updated for
  each request on a warm instance. Format is
  `00-<trace_id 32 hex>-<span_id 16 hex>-<flags 2 hex>`, e.g.
  `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01`.
- **Lifetime**: the trace_id is minted at the gateway (or carried
  in from the inbound `traceparent`); the span_id identifies the
  specific request's `gateway.handler` span. A warm instance receives
  fresh headers for every request.

For long-lived HTTP servers, use the SDK's HTTP server instrumentation
to extract `traceparent`, `tracestate`, and `baggage` from each request.
Do not use `process.env.TRACEPARENT` (or expect `TRACESTATE`/`BAGGAGE`
environment variables) for per-request correlation.

You do not need to read or write `TRACEPARENT` for the platform's
own spans to work — the platform's `sched.wake`, `vmmd.create_*`,
`guest.resume`, and `guest.readiness` spans are joined on the same
trace_id automatically (issue #555 layer 3, merged).

## Auto-instrumentation: Node 22 / 24

Add the OTel SDK and the auto-instrumentation hooks to your app's
`dependencies`. The HTTP auto-instrumentation extracts the forwarded
W3C headers for each request and joins the handler's child spans to the
platform trace.

```json
// package.json
{
  "dependencies": {
    "@opentelemetry/api": "^1.9.0",
    "@opentelemetry/auto-instrumentations-node": "^0.52.0",
    "@opentelemetry/exporter-trace-otlp-http": "^0.55.0",
    "@opentelemetry/sdk-node": "^0.55.0"
  }
}
```

```js
// tracing.js — required: OTel SDK must be initialized BEFORE any
// instrumented module (express, http, pg, etc.) is required.
const { NodeSDK } = require('@opentelemetry/sdk-node');
const { getNodeAutoInstrumentations } = require('@opentelemetry/auto-instrumentations-node');
const { OTLPTraceExporter } = require('@opentelemetry/exporter-trace-otlp-http');
const { Resource } = require('@opentelemetry/resources');

const sdk = new NodeSDK({
  resource: new Resource({ 'service.name': process.env.FAAS_APP_SLUG || 'app' }),
  traceExporter: new OTLPTraceExporter({
    // The platform forwards the env var to the runner; you can
    // override via app.json's `env` block.
    url: process.env.OTEL_EXPORTER_OTLP_ENDPOINT,
  }),
  instrumentations: [getNodeAutoInstrumentations()],
});
sdk.start();
process.on('SIGTERM', () => sdk.shutdown());
```

> **Why this works:** the Node HTTP instrumentation extracts the
> `traceparent`, `tracestate`, and `baggage` headers from each inbound
> request. Set `OTEL_PROPAGATORS=tracecontext,baggage` if your image
> overrides the SDK default; do not rely on the process-scoped
> `TRACEPARENT` seed for warm requests.

```js
// handler.js — your existing handler, unchanged. The auto-
// instrumentation will create child spans for each HTTP route,
// database call, and outbound fetch under the platform's
// trace_id.
const express = require('express');
const app = express();
app.get('/', (req, res) => res.send('hi'));
app.listen(process.env.PORT || 8080);
```

```json
// app.json — pin the OTLP endpoint for your collector
{
  "env": {
    "OTEL_EXPORTER_OTLP_ENDPOINT": "http://otel-collector.faas.svc:4318"
  }
}
```

That's it. A request to your function now shows up in your
collector as a single trace with one parent (`gateway.handler`)
and a tree of child spans: `http.server` (your route handler) →
`pg.query` (if you hit a database) → `http.client` (any outbound
fetch).

## Auto-instrumentation: Python 3.12 / 3.13

```toml
# pyproject.toml
[project]
dependencies = [
  "opentelemetry-distro[otlp]>=0.48b0",
  "opentelemetry-instrumentation>=0.48b0",
]
```

```bash
# build step
pip install opentelemetry-bootstrap
opentelemetry-bootstrap -a install
```

```bash
# Procfile or app.json's `command` — bootstrap must run BEFORE
# your handler imports.
export OTEL_PROPAGATORS=tracecontext,baggage
exec opentelemetry-instrument \
  --service_name "${FAAS_APP_SLUG}" \
  --exporter_otlp_endpoint "${OTEL_EXPORTER_OTLP_ENDPOINT}" \
  --exporter_otlp_protocol http/protobuf \
  gunicorn app:app
```

The `opentelemetry-instrument` wrapper enables HTTP instrumentation
that extracts the forwarded W3C headers from each request and joins
Flask/FastAPI/Django/psycopg spans to the platform's trace. We set
`OTEL_PROPAGATORS=tracecontext,baggage` explicitly so a custom
propagator in the parent image doesn't silently drop the join.

## What the platform does NOT do

- **No library pre-installation.** The runner image ships with
  the standard library only. You bring your own OTel SDK. The
  runner sandbox is small (130 MB fleet target, ADR-040); we do
  not pay for a 30 MB SDK on disk per app when most apps will
  not opt in.
- **No auto-detection.** Set `OTEL_EXPORTER_OTLP_ENDPOINT` in
  `app.json`'s `env` (or rely on the platform's default if the
  operator has set one at the cluster level) to turn on export.
  Without it, spans are still joined to the platform's
  `gateway.handler` trace and visible via
  `GET /v1/traces/{trace_id}` — you just don't get the
  collector-side view.
- **No head-based sampling override.** The platform samples
  100% for the first 100 root spans of every new deployment
  (acceptance #5), then falls back to the head ratio in
  `OTEL_TRACES_SAMPLER_ARG` (default 1.0). Your handler's
  child spans are NOT subject to the platform's sampler — the
  parent's `SampledFlag=true` is what reaches your SDK, so
  every child span you create is recorded.

## Cross-daemon trace query

If you have observer access to the box, `GET /v1/traces/{trace_id}`
returns the full span tree for any wake within the last 24 hours
(PR #617 ring buffer, default 100k entries). The shape is the
[`Trace` schema](../../api/openapi.yaml) — every span carries
`trace_id`, `span_id`, `parent_span_id`, `name`, `start_time`,
`end_time`, `status`, and an `attributes` map (`app_id`,
`deployment_id`, `instance_id`, etc.).

```bash
curl -sH "X-Faas-Trace-Auth: $OBSERVER_TOKEN" \
  https://faas.example.com/v1/traces/4bf92f3577b34da6a3ce929d0e0e4736 | jq
```

## See also

- `docs/faas_implementation_spec.md` §16 — tracing contract
- `docs/adr/` — architectural decisions (PR #617 / issue #555)
- W3C TraceContext: https://www.w3.org/TR/trace-context/

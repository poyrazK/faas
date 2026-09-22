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
- **Response correlation**: the public edge returns
  `X-Gregale-Trace-Id` with the canonical 32-character trace id and sends the
  same platform-owned header to the guest. Use that value with
  `gregale trace <trace-id>` to query the account trace index for retained,
  redacted evidence across the apps in your account.
- **Lifetime**: the trace_id is minted at the gateway (or carried
  in from the inbound `traceparent`); the span_id identifies the
  specific request's `gateway.handler` span. A warm instance receives
  fresh headers for every request.

For long-lived HTTP servers, use the SDK's HTTP server instrumentation
to extract `traceparent`, `tracestate`, and `baggage` from each request.
Do not use `process.env.TRACEPARENT` (or expect `TRACESTATE`/`BAGGAGE`
environment variables) for per-request correlation.

## Deployment identity

Every wake, restore, and migration stamps the workload with reserved
`FAAS_*` variables. They are platform-owned and cannot be overridden by an
image environment variable, app env row, or secret:

`FAAS_APP_ID`, `FAAS_DEPLOYMENT_ID`, `FAAS_TENANT_ID`,
`FAAS_INSTANCE_ID`, `FAAS_NODE_ID`, `FAAS_REGION`, `FAAS_COMMIT_SHA`,
`FAAS_DEPLOYMENT_TAG`, `FAAS_DEPLOYMENT_CREATED_AT` (RFC 3339), and
`FAAS_IMAGE_DIGEST` when the deployment has an immutable image/artifact
digest.

The gateway also forwards the request-scoped `X-Faas-Request-Id`, app,
deployment, tenant, instance, node, region, commit, deployment-tag,
deployment-created-at, and image-digest headers when those values are
available. Combine those headers with the W3C context above in application
middleware. Platform request telemetry, errors, traces, and streamed/log-drain
records retain the same deployment identity automatically.

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

## Platform-owned outbound integrations

Requests sent through Gregale's configured outbound integrations
(`/i/{integration_id}/...`) are instrumented by `outboundd` without any
application SDK setup. The platform emits a binding span named
`gregale.outbound.integration` and a child HTTP client span with the method,
destination host, response status, network lifecycle events, duration, and
error state. W3C trace context is injected into the provider request, so a
caller that already has an active OTel context remains connected to the
provider span. The platform outbound client also injects that context
automatically.

Integration ID, attached app ID, origin host, and origin scheme are bounded
attributes. Request paths, query strings, bodies, credentials, and provider
headers are intentionally excluded from spans. This covers the platform-owned
binding path; arbitrary direct guest `fetch()` calls still require runtime
instrumentation or a future transparent egress observation layer.

## Platform-owned managed bindings

Managed Postgres and object-storage control-plane calls are also traced by the
platform when Gregale owns the provider client. Neon, S3-compatible, and GCS
requests produce child HTTP dependency spans with the binding type and
provider, destination host, method, response status, network lifecycle
events, duration, and error state. Neon requests additionally sit beneath a
`gregale.binding.managed_postgres` span, so the platform-owned binding is
visible between the request and the provider API.

The same redaction boundary applies: bucket names, object keys, database
project identifiers, query strings, request bodies, authorization material,
and provider headers are not span attributes. Customer code that opens its own
database or storage client remains outside the platform-owned path and needs
the runtime's OpenTelemetry instrumentation.

Guest-to-guest HTTP calls through the node-local service proxy are represented
as `managed_binding/service_proxy` client spans named `service.<name>`. Gregale
records the bounded service name, target app ID, method, response status, and
elapsed time; retries remain inside the same dependency span, with the existing
guest-transport span beneath it. Paths, queries, headers, bodies, and caller
credentials are not recorded. The proxy injects the dependency span's W3C
context into the target guest request, so neither application needs an
OpenTelemetry SDK for the service call and target execution to be visible.

Authorized service-proxy spans are also sent through Gregale's trusted
telemetry channel and attached to the matching retained request in the
production debugger. This path is enabled by default and does not require a
customer API key, a customer OTLP exporter, or an operator-configured external
collector. `FAAS_OTEL_SPANS_WRITER_ENABLED=false` disables both the customer
OTLP writer and this platform-owned retention path; `FAAS_OTEL_FLUSH_INTERVAL`
controls their coalescing cadence (30 seconds by default).

When the caller propagates its inbound `traceparent`, the service dependency is
nested under that original request and appears in the same waterfall. When the
header is absent, Gregale starts a separate service-call trace. It deliberately
does not infer a parent from source instance and timing: an instance can process
concurrent requests, so that heuristic could attach a payment or notification
call to the wrong customer request. Because the durable debugger enriches an
existing request row by trace ID, that separate trace is not shown under the
original request; exact cross-service nesting still requires the caller to
propagate the inbound W3C context.

## Event-triggered invocations

Event-driven invocations preserve W3C trace context carried by a broker record's
`traceparent`/`tracestate` headers. The platform joins the source event to the
dispatch path with bounded spans for `gregale.trigger.poll`,
`gregale.trigger.dispatch`, `gregale.trigger.batch`, and
`gregale.invocation`. Retry and dead-letter outcomes are represented by
`gregale.trigger.retry` and `gregale.trigger.dlq` spans.

The schedd uses the first valid record context as the batch parent and records a
small capped set of additional record contexts as span links when a batch mixes
producers. If a broker record has no valid W3C context, the platform still emits
the trigger spans from the scheduler cycle. Event payloads, item identifiers,
header values, and credentials are not span attributes.

Queue messages sent through `POST /v1/apps/{slug}/queues/send` also carry the
producer's W3C context and the canonical `X-Gregale-Trace-Id` in the durable
invocation envelope. A delivery restores that context before dispatch, so the
queue hop remains part of the same trace. `GET /v1/account/traces/{trace_id}`
returns the retained request evidence plus a safe queue lifecycle projection;
`gregale trace <trace-id>` renders both views without client-side app fan-out.

## Platform-to-guest transport

The vmmd HTTP bridge emits a platform-owned client span for the host-side
transport into the selected guest instance. The span records the bridge kind,
wire protocol, guest application protocol, bounded guest port, instance ID,
response status, duration, and transport errors. It does not record request
paths, query strings, headers, bodies, or credentials.

This span covers the platform-to-guest hop only. Direct guest-to-internet TCP
flows bypass the HTTP bridge; observing those flows requires a separate
kernel-level flow telemetry implementation and is not inferred from this span.

## What the platform does NOT do

- **No library pre-installation.** The runner image ships with
  the standard library only. You bring your own OTel SDK. The
  runner sandbox is small (130 MB fleet target, ADR-040); we do
  not pay for a 30 MB SDK on disk per app when most apps will
  not opt in.
- **No auto-detection.** Set `OTEL_EXPORTER_OTLP_ENDPOINT` in
  `app.json`'s `env` (or rely on the platform's default if the
  operator has set one at the cluster level) to turn on export.
  Without it, the platform's gateway span is still available in
  the local trace ring, but customer-created child spans are not
  persisted by `GET /v1/traces/{trace_id}`. Export to the platform
  OTLP endpoint is required for customer spans to reach the
  configured collector. This limitation applies to customer-created spans;
  Gregale-owned service-proxy spans use the automatic retained path described
  above.
- **No head-based sampling override.** The platform samples
  100% for the first 100 root spans of every new deployment
  (acceptance #5), then falls back to the head ratio in
  `OTEL_TRACES_SAMPLER_ARG` (default 1.0). Your handler's
  child spans are NOT subject to the platform's sampler — the
  parent's `SampledFlag=true` is what reaches your SDK, so
  every child span you create is recorded.

## Platform trace query

If you have observer access to the box, `GET /v1/traces/{trace_id}`
returns the gatewayd-public platform span tree retained by that
instance's bounded in-memory ring (24 hours or 100k entries, whichever
comes first). It is not a durable or fleet-wide trace store. The shape
is the [`Trace` schema](../../api/openapi.yaml) — every platform span carries
`trace_id`, `span_id`, `parent_span_id`, `name`, `start_time`,
`end_time`, `status`, and an `attributes` map (`app_id`,
`deployment_id`, `instance_id`, etc.).

Customer-created spans require OTLP export and are inspected in the
configured collector. The customer OTLP ingest path summarizes those
spans into request telemetry rather than adding them to this local ring.

```bash
curl -sH "X-Faas-Trace-Auth: $OBSERVER_TOKEN" \
  https://faas.example.com/v1/traces/4bf92f3577b34da6a3ce929d0e0e4736 | jq
```

## See also

- `docs/faas_implementation_spec.md` §16 — tracing contract
- `docs/adr/` — architectural decisions (PR #617 / issue #555)
- W3C TraceContext: https://www.w3.org/TR/trace-context/

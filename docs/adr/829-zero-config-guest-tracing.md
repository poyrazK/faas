# ADR-829: Zero-config in-guest tracing for the debugger

- **Status:** accepted for internal implementation; production acceptance pending
- **Date:** 2026-10-09
- **Amends:** [ADR-127](127-production-debugger.md) §5 (customer OTel ingest)
- **Related:** ADR-819 (continuous CPU profiling; same guest bridge and broker shape)
- **Decision:** An app opts in with `tracing: {enabled: true}`. guest-init runs
  an OTLP/HTTP receiver on the standard local endpoint `127.0.0.1:4318`, stamps
  the standard `OTEL_*` exporter environment, and preloads pinned Node and
  Python auto-instrumentation on managed runtimes. Export batches cross a
  dedicated vsock port; vmmd adds the host-owned identity without parsing them,
  and apid decodes, rate-limits and merges the spans into `request_telemetry`.

## Context

ADR-127 made request telemetry, regressions, compare and the rules-based
evidence synthesis available, but the "why" behind a slow request depends on
spans from inside the VM. Today those arrive only from platform-owned
dependencies (managed bindings, outbound integrations) or from customers who
configure an OTel exporter against the public `/v1/otel/v1/traces` endpoint
with an API key. Nothing in the guest helps: a customer's own PostgreSQL,
Redis or third-party HTTP calls are invisible unless they wire OTel by hand
and put a credential into their app. The ADR-127 example ("PostgreSQL 82 ms →
191 ms") is therefore out of reach for most apps.

The gateway already injects a W3C `traceparent` into every guest-bound request
(`pkg/gateway/trace_propagation.go`) and keys `request_telemetry` rows on the
same trace ID. Any span the app emits under that context can be attached to
the row without new correlation machinery.

## Decision

### 1. Opt-in manifest setting

`api.TracingConfig{enabled, sample_ratio}` is accepted at the top level of
`gregale.yaml`/`gregale.toml`, in `lifecycle`, and on app create/update. It is
baked into the next image like `profiling`; changing it invalidates snapshots.
Enabling requires a plan with `DebugTelemetryEnabled`, and apid refuses it
until the operator turns the broker on with `FAAS_GUEST_TRACING_ENABLED=1`
(the same operator opt-in shape as `FAAS_PROFILING_ENABLED`). `sample_ratio`
defaults to 1 and must be in (0, 1].

### 2. Guest bridge

guest-init listens on `127.0.0.1:4318` — the OTLP/HTTP default, so SDKs that
are already present need no endpoint configuration — and accepts
`POST /v1/traces` with protobuf or JSON bodies, optionally gzip-encoded, up to
`TraceMaxFrameBytes`. It forwards the raw body with a one-byte codec header
over vsock port `TraceVsockPort` (1041) and waits for a one-byte ack. The
bridge holds no credential. A failed bind (the app already owns 4318) disables
tracing for that instance with a warning; it never fails boot.

An app that already configures its own trace exporter
(`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` or
`OTEL_TRACES_EXPORTER` in its environment) is left completely untouched.
Otherwise guest-init stamps:

- `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://127.0.0.1:4318/v1/traces`,
  `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf`, `OTEL_TRACES_EXPORTER=otlp`
  and `FAAS_TRACING_ENABLED=1`, which gates the runtime bootstraps
- `OTEL_METRICS_EXPORTER=none`, `OTEL_LOGS_EXPORTER=none` unless the app set them
- `OTEL_TRACES_SAMPLER=always_on`, or `traceidratio` with the configured ratio,
  unless the app chose a sampler. The default deliberately ignores the
  parent's sampled flag: the gateway's own span is head-sampled for platform
  tracing, which must not suppress the customer's request spans.
- `NODE_OPTIONS=--require=/opt/gregale/tracing/node.cjs` and
  `PYTHONPATH=/opt/gregale/tracing/python:…` when those bootstrap assets exist.

guest-init does not know the app slug, so `OTEL_SERVICE_NAME` is left to the
SDK default; the debugger keys spans by trace, not service name.

### 3. Managed runtime auto-instrumentation

Runner images for Node 22/24 and Python 3.12/3.13 contain pinned OTel SDKs and
auto-instrumentation under `/opt/gregale/tracing`, installed from lockfiles
like the ADR-819 profiling collectors (Node: `@opentelemetry/sdk-node`
0.222.0 with `auto-instrumentations-node` 0.80.0; Python: SDK 1.44.0 with
0.65b0 instrumentations for WSGI/ASGI frameworks, PostgreSQL drivers,
SQLAlchemy, Redis, MongoDB and HTTP clients). Instrumentation loads before the
init snapshot, so restored instances pay no startup cost; only cold boots do.
Custom images, Dockerfile builds and Go apps still benefit from §2: any OTel
SDK the app ships exports to the local endpoint with no token.

Both bootstraps are diagnostics-only: a missing or incompatible package
leaves the app running untraced. The Node bootstrap installs no signal
handlers, because a SIGTERM listener would replace Node's default exit and
stall guest-init's graceful stop. OpenTelemetry Python is a namespace package,
so mixing versions across `sys.path` breaks imports (an app's library may pull
in an older `opentelemetry-api`). The image therefore installs the
OpenTelemetry distributions into `lib/`, which the bootstrap prepends as one
coherent set, and their third-party dependencies into `deps/`, which it
appends so the app's protobuf, requests or wrapt always win. An app that ships
its own `opentelemetry-sdk` configures tracing itself and is skipped.

Platform `sitecustomize` bootstraps (restore reseed, profiling, tracing) are
stacked on `PYTHONPATH`; each now chains only to entries after its own
directory. Excluding only its own directory, as before, found an earlier
bootstrap again: reseed and profiling together already recursed until
`RecursionError`, so a profiling-enabled app's own `sitecustomize` never ran.

### 4. Host path: vmmd brokers, apid parses

vmmd registers a stream handler on `TraceVsockPort`, bounds concurrency and
frame size, and resolves account, app and deployment from its live instance
map and the app row. It refuses frames for apps whose manifest does not enable
tracing. It never decompresses or decodes OTLP (ADR-819 broker rule: the root
daemon only bounds and stamps identity). It forwards the raw body and the
host-owned principal to apid's existing SpansWriter Unix socket via a new
`IngestGuestSpans` RPC. v1 supports the single-box socket only; split-box
compute nodes first need a vmmd client identity for apid's private mTLS
listener, which is a follow-up.

apid checks the account's plan (`DebugTelemetryEnabled`), takes from the
existing per-account `DebugTelemetryRequestsPerMinute` bucket, bounds buffered
traces, decodes with the same codec as the public endpoint (4 MiB decoded
bound), and adds spans to one process-wide `SpansAccumulator` shared by both
SpansWriter listeners. Its flush loop writes back through the receiver's own
`WriteSpansSummary`, so guest spans share that path's validation, rate cap and
outcome metrics; `UpdateSpansSummary` already merges by span identity across
producers, pins `account_id`, and keeps the slowest Scale-tier maximum.

## Consequences

- Opted-in managed-runtime apps get DB, cache and HTTP client spans on the
  debugger evidence view without code changes or credentials.
- Spans are attached only to traces that have a `request_telemetry` row.
  Collapsed rows keep one representative trace per bucket (ADR-127 §2), so
  spans for other traces in that bucket are discarded by the zero-row UPDATE.
  This is exemplar semantics, bounded by the existing rate limit.
- A guest can only attach spans within its own account: the account comes from
  vmmd, the accumulator rejects cross-account trace claims, and the SQL pins
  `account_id`. Within an account, a guest can annotate another app's trace,
  the same authority an account API key already has on the public endpoint.
- Background spans without an inbound request context have fresh trace IDs and
  match no row; they cost bridge and rate-limit budget only.
- OTel ID generators are not reseeded on restore. Request spans inherit the
  gateway's unique trace ID, so span ID reuse across restored instances cannot
  collide inside one trace's summary.
- Pre-fork servers (gunicorn) need the usual OTel post-fork initialization;
  the bootstrap does not attempt to patch worker models.

## Rejected alternatives

- **Inject an API key and use the public endpoint.** Puts a credential in every
  guest and routes span traffic through the public edge.
- **Decode OTLP in vmmd.** Adds an untrusted protobuf/gzip parser to the only
  root daemon, against the ADR-819 broker rule.
- **Infer dependency latency from egress flows.** The egress flow log records
  destinations only, and time-window attribution is wrong under concurrency.
- **Auto-instrument every app by default.** Startup and compatibility risk for
  bundled or unusual apps; revisit after the opt-in has native acceptance.

## Validation

Unit: manifest/plan validation, env stamping precedence, bridge size, codec
and ack handling, vmmd principal refusal, apid plan/rate/decoding outcomes and
accumulator merge. `make test-runtime-bootstraps` checks that both preloads
never block startup, that stacked `sitecustomize` files chain exactly once,
the Python `lib`/`deps` ordering, and that the Node preload keeps the default
SIGTERM exit.

Local end-to-end (pinned packages, outside a VM): a Node HTTP app and a Python
WSGI app, each calling a downstream HTTP dependency under the bootstraps with
an unsampled inbound `traceparent`, export server and client spans carrying
the gateway's trace ID; the Python run also passes with an older
`opentelemetry-api` already on the path.

Native acceptance (pending): a Node and a Python app with `tracing.enabled`
serving a request that queries PostgreSQL shows the client span under
`gregale debug requests get`.

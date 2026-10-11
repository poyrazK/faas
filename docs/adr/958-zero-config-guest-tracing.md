# ADR-958: Zero-config in-guest tracing for the debugger

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

Migration `20261009183654862_debug_regression_suspected_dependency.sql` retains its published ADR-934 header to preserve migration bytes. This tracing decision is now ADR-958; ADR-934 remains the durable entity guest outbox decision.

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
host-owned principal to apid's existing SpansWriter service via a new
`IngestGuestSpans` RPC. Single-box hosts use the local Unix socket. Split-box
compute nodes dial apid's private mTLS listener — the one gatewayd-internal
already uses for spans and request telemetry — with vmmd's existing
`vmmd/apid-client` leaf. That leaf carries the node identity
(`pkg/pki.RoleUsesNodeIdentity`: CN = `compute_nodes.name`), so apid's node
verifier admits it without new PKI. The `vmmd_service` role renders
`99-faas-spans-writer.conf` from the manifest-derived
`faas_gatewayd_app_errors_target`; vmmd refuses a remote target without
client TLS rather than sending spans in plaintext.

On that shared listener any active compute node can submit spans for any
account, exactly as gatewayd-internal on the same node already can through
`WriteSpansSummary`. Binding each export to an instance scheduled on the
authenticated node (via `NodeIdentityResolver`) would tighten both producers
together and is left to a follow-up.

apid checks the account's plan (`DebugTelemetryEnabled`), takes from the
existing per-account `DebugTelemetryRequestsPerMinute` bucket, bounds buffered
traces, decodes with the same codec as the public endpoint (4 MiB decoded
bound), and adds spans to one process-wide `SpansAccumulator` shared by both
SpansWriter listeners. Its flush loop writes back through the receiver's own
`WriteSpansSummary`, so guest spans share that path's validation, rate cap and
outcome metrics; `UpdateSpansSummary` already merges by span identity across
producers, pins `account_id`, and keeps the slowest Scale-tier maximum.

### 5. Dependency attribution for app spans

Spans an app emits use OpenTelemetry semantic conventions, not the
platform-owned `gregale.dependency.type` attribute, so the debugger used to
treat them as anonymous `application` spans grouped by raw span name (every
HTTP client call is named `GET`). `pkg/debugger` now classifies them as
`app_dependency` with a kind and a bounded grouping name:

| Signal | Kind | Name |
|---|---|---|
| `db.system(.name)` | the database system (`postgresql`, `redis`, …) | operation and table/collection (`SELECT orders`); key-value stores report only the command |
| client span with `http.request.method` | `http` | destination host only |
| client span with `rpc.system` | the RPC system | `service/method` |
| client/producer span with `messaging.system` | the messaging system | operation and destination |

Names never carry literals, keys, paths, query strings, ports or userinfo;
the name is derived from the redacted statement's leading keyword and table
identifier. Platform-owned classifications keep precedence. The identity
reaches the API as `dependency_name` and keys every dependency and
critical-path rollup.

The dependency history gains `deployment_comparison` (newest deployment
versus the one before it) and each request's evidence gains
`dependency_comparison` for its route (its deployment versus the previous
one). Both reuse the history rollup with the previous deployment as baseline
and the same regression thresholds (≥ 5 calls each side, ≥ 1.5× and
≥ 25 ms p95). When a dependency regressed, the rules-based synthesis reports
diagnosis `dependency_regression` with a headline such as
`postgresql "SELECT orders" slowed from 82ms to 191ms p95 since the previous
deployment (v80)`.

The regression detector applies the same comparison when it opens or
re-confirms a route regression: it compares the route between the
detector's own current and baseline deployments and stores the top
regressed classified dependency (never an anonymous application span) in
`debug_regression_observations.suspected_dependency`, a bounded jsonb
object (≤ 1 KiB, CHECKed). The regressions API, `gregale debug regressions`
(SUSPECTED column), the live `debug_regression_changed` event and the
`debug.regression.detected`/`resolved` webhooks carry it, so the alert names
the dependency without anyone opening request evidence. A pass that cannot
compute a suspect keeps the previously stored one; span reads are bounded
and best-effort and never block recording the regression.

Customer-submitted spans (the public OTLP endpoint and the in-guest bridge)
have the platform-reserved `gregale.*` attributes stripped at ingest, so an
app cannot present its own spans as managed bindings, outbound integrations
or guest transport. Platform producers use the retained service-spans
exporter, which is not affected.

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

Native (2026-10-09, `gregale-internal-test-1`, nested KVM — a functional
check, not a CLAUDE.md acceptance host): `TestGuestTracingMetal` passed twice.
A scratch image whose server carries an OTel SDK but no tracing configuration
was deployed with `tracing.enabled`, woken from its init snapshot by one
request with an unsampled `traceparent`, and its `SELECT orders` client span
appeared on `GET /v1/apps/{slug}/debug/requests/{id}/evidence`. This covers
guest-init env stamping, the bridge across snapshot restore, vsock 1041, the
vmmd broker, apid ingest and the flush into `spans_summary`. During the init
cold boot, before the instance is serving, the broker refuses exports
(`identity`); that is by design.

`TestGuestTracingNodePreloadMetal` (2026-10-10, same host) passed: a plain
`node:22-alpine` app with no tracing code, carrying the pinned
`/opt/gregale/tracing` bundle, was preloaded by guest-init's `NODE_OPTIONS`,
and its HTTP client span reached the evidence classified as
`app_dependency`/`http` after a snapshot-restore wake. Both scenarios left no
VM resources behind. The Node bundle adds about 111 MB (uncompressed) to the
shared read-only runner base; per-app layers are unaffected.

Still pending: the same checks on a dedicated native host, the Python
preload inside a VM (validated outside a VM only), and the split-box mTLS
path on a two-node layout.

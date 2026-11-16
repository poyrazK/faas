# Request tracing

Request tracing shows what happened inside your app during a slow or failed
request: database queries, cache calls and outbound HTTP requests, each with
its duration, attached to the request in the production debugger. Turn it on
with one setting. You don't need an API key, an exporter configuration or any
code changes.

```yaml
# gregale.yaml
tracing:
  enabled: true
```

```toml
# gregale.toml
[tracing]
enabled = true
```

The setting applies from the next deployment. Start an investigation with
[`gregale debug status <app>`](debug-status.md), then open a request with
`gregale debug requests get <app> <request-id>` or in the dashboard debugger;
spans from your app appear under the request, and the evidence summary uses
them to point at the slow dependency.

## What the debugger does with spans

Database, cache, HTTP, RPC and messaging calls are recognised from standard
OpenTelemetry attributes and grouped by what they call: `postgresql
"SELECT orders"`, `redis "GET"`, `http "api.stripe.com"`. Grouping names
never include query values, keys, URL paths or credentials.

For each route the debugger compares these dependencies on the current
deployment with the previous one. When one slows down, the request evidence
says so directly, for example:

> postgresql "SELECT orders" slowed from 82ms to 191ms p95 since the previous deployment (v80).

The same comparison is available for the whole app under dependency latency
(`deployment_comparison`).

Database dependencies are also broken down by query. Each statement is
normalized into a pattern: literal values, placeholders, `IN` lists and
multi-row `VALUES` are folded, so `WHERE id = 42` and `WHERE id = 43` count as
one query. Normalization also folds dollar-quoted and hex literals, quoted
strings compared or listed as values, and every value in a JSON-shaped
statement (MongoDB and similar); cache commands (Redis, Valkey, KeyDB,
Dragonfly, memcached) keep only the verb, `GET ?`, never the key.

Only the attributes the debugger needs are stored with a span: the dependency
classification keys (`db.system`, `server.address`, `rpc.service`, …),
failure classes (`error.type`, `exception.type`) and status codes. Every other
attribute your instrumentation records, such as user ids or request headers,
is dropped at ingestion, and `url.full` / `http.url` keep only the origin
(`https://api.stripe.com`), never the path or query. `gregale debug bundle`
leaves log messages and SQL statements out of the bundle it writes.

When a dependency regresses, the evidence and the regression alert
name the query that changed:

> postgresql "SELECT orders" p95 rose from 82ms to 191ms (2.3x) compared with the previous deployment (v80). The slower query is `SELECT * FROM orders WHERE customer_id = ?` (p95 40ms to 210ms).

Failures are compared the same way. A span that ends with error status counts
as a failed call, and its failure class is kept: the `error.type` attribute,
or the type of the first recorded exception (`QueryTimeout`, `503`). Exception
messages and stack traces are never retained, since they often contain request
data. When a request fails while one of its dependency calls failed, the
evidence names that call:

> The request failed while its call to postgresql "SELECT orders" failed with QueryTimeout.

A dependency that fails materially more often than on the previous deployment
is flagged as `failure_regression`. That needs at least 3 failed calls, and an
error rate at least 5 percentage points and twice above baseline:

> http "api.stripe.com" fails 12.5% of calls since the previous deployment (v80), up from 0.0%.

## Request logs

With tracing enabled, the app's own log lines are linked to the request that
wrote them. Python's standard `logging` records and Node's pino and bunyan
logs are exported automatically with the active trace context, and the
request's evidence shows its last 20 lines in the dashboard and the API:

```
12:04:31.118  error  runtime  charge failed for [REDACTED:email]: card declined
```

Python sends the records your logging configuration emits (warnings and
above unless you set a lower level), and your console output is unchanged.
Lines are redacted (emails, card numbers, tokens and keys) and cut to 512
characters; structured log fields are not stored. Plain `console.log` output
and winston logs are not linked. Log lines without a request (for example at
startup) still appear in your runtime logs as before.

To keep your logs out of Gregale, set `OTEL_LOGS_EXPORTER=none`, or point
`OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` at your own collector.

## Following a request across services

`gregale debug trace <trace-id>` shows every retained request of one trace across
your apps, with the logs, queued invocations and spans linked to it. The trace
id is on each request in `gregale debug requests get`.

```text
MATCHES
  public-api · GET /checkout · HTTP 200 · 920 ms · telemetry row …
  billing · POST other · HTTP 200 · 900 ms · telemetry row …
      woke by restore in 610 ms (wake …) · app ≈270 ms · other ≈20 ms
```

A call to another service joins the trace when the calling code forwards the
`traceparent` header of the request it is handling. With tracing on, the
Node.js and Python instrumentation does this for the HTTP clients it supports.

When the caller forwards nothing, Gregale links the call itself if the calling
instance is serving exactly one request at that moment. The hop is shown as
"trace linked by Gregale" and returned with `trace_inferred: true`, so it can
be told apart from propagated context. An instance serving several requests at
once (or holding a WebSocket or TCP session) is not linked, because Gregale
cannot tell which request made the call; forward `traceparent` yourself for
busy services. A call made by background work while exactly one request is in
flight is linked to that request.

For a request that found its app parked, the line under it says how the app
was woken (restored from a snapshot or cold booted) and how long the instance
took to become ready. When the app's runtime reports its own execution time,
as the managed function runtimes do, that is shown as app time, and the rest
(routing, queueing and the network) as other. Latencies are kept rounded up to
10 ms below 1 s and more coarsely above, so the split is approximate.

## What is traced

On the managed Node.js (22, 24) and Python (3.12, 3.13) runtimes, Gregale
loads OpenTelemetry auto-instrumentation for you:

- **Node.js:** HTTP servers and clients, Express, Fastify, Koa, NestJS,
  PostgreSQL (`pg`), MySQL, MongoDB, Redis, gRPC and the other libraries
  supported by `@opentelemetry/auto-instrumentations-node`.
- **Python:** WSGI/ASGI, Django, Flask, FastAPI, `psycopg2`, `psycopg`,
  `asyncpg`, SQLAlchemy, Redis, PyMongo, `requests`, `urllib3`, `httpx` and
  `aiohttp` clients.

Filesystem, DNS and raw socket activity is not traced.

### Go

Go apps are compiled, so nothing can be loaded into them at start. The Go
SDK's `tracing` package gives the same request, HTTP client and database
spans without an OpenTelemetry dependency:

```go
import "github.com/gregale-dev/gregale-go/preview/tracing"

func main() {
	shutdown := tracing.Start()
	defer shutdown(context.Background())

	http.DefaultTransport = tracing.Transport(http.DefaultTransport)
	db, err := tracing.Open("pgx", os.Getenv("DATABASE_URL"), "postgresql")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.QueryContext(r.Context(), "SELECT id FROM orders WHERE id = $1", r.PathValue("id"))
		...
	})
	log.Fatal(http.ListenAndServe(":"+os.Getenv("PORT"), tracing.Handler(mux)))
}
```

- `tracing.Handler` records each request and continues the trace the gateway
  started, so the spans attach to the request in the debugger. Wrap the
  router, and the span is named after the matched route (`GET /orders/{id}`).
- `tracing.Transport` records outgoing HTTP calls and passes the trace on in a
  `traceparent` header. URLs are recorded without query strings or
  credentials.
- `tracing.Open` (or `tracing.WrapConnector` with `sql.OpenDB`) records every
  query run with a context, for any `database/sql` driver. The last argument
  names the database: `postgresql`, `mysql`, `sqlite` and so on.
- `tracing.StartSpan(ctx, "name")` records your own operations.

Pass the request's context to outgoing calls (`http.NewRequestWithContext`,
`QueryContext`, `ExecContext`); a call made with `context.Background()`
starts a new trace and does not appear under the request. For functions, wrap
the handler you pass to `function.Serve`: `function.Serve(tracing.Handler(mux))`.

The package does nothing unless `tracing.enabled` is on for the app, so the
same binary runs unchanged locally and in tests.

### Other apps

Dockerfile builds, container images and Go apps that already use OpenTelemetry
can export spans with the SDK they include. Inside the app, the standard
OTLP/HTTP endpoint `http://127.0.0.1:4318/v1/traces` accepts them; Gregale
sets the standard `OTEL_EXPORTER_OTLP_TRACES_*` variables so most SDKs need no
configuration.

SDKs and collectors that export over gRPC (the default for .NET and many
OpenTelemetry Collector setups) can use `127.0.0.1:4317` instead, without TLS.
It accepts the same trace and log exports, uncompressed or gzip-compressed.

## Sampling

By default every request's spans are exported. To reduce volume, set a ratio:

```yaml
tracing:
  enabled: true
  sample_ratio: 0.25
```

Exports have their own rate limit, the same per-minute rate as your plan's
request telemetry, so tracing never uses up the budget your request records
need. Spans that exceed it are dropped and the app keeps serving. An app in an
organization that owns its resources is limited by the organization's plan,
not the plan of whoever created the app.

## Sending traces to your own backend as well

To keep the debugger and also see every span and log line in your own
OpenTelemetry backend (Grafana, Honeycomb, Datadog, a collector you run), set
`tracing.forward`:

```yaml
tracing:
  enabled: true
  forward:
    endpoint: https://otlp.example.com
    headers_secret: OTLP_HEADERS
```

```sh
gregale env set --app my-api OTLP_HEADERS='Authorization=Bearer%20<token>'
```

- Each trace and log export your app makes (and metric export, with
  `tracing.metrics`) is sent unchanged to `<endpoint>/v1/traces`,
  `<endpoint>/v1/logs` or `<endpoint>/v1/metrics` over OTLP/HTTP, from inside
  the app's instance, in addition to Gregale. Spans keep everything the SDK
  recorded, including the attributes, events and links the debugger does not
  keep.
- `endpoint` is the base URL, as in `OTEL_EXPORTER_OTLP_ENDPOINT`: `http://`
  or `https://`, without credentials, query string or fragment. An `http://`
  endpoint on your private network (for example a collector app) works too.
- `headers_secret` is optional and names an app secret holding the request
  headers in the `OTEL_EXPORTER_OTLP_HEADERS` form: comma-separated
  `key=value` pairs, with values percent-encoded. The secret is read when an
  export is sent, so a changed value applies from the instance's next wake. If
  the secret is missing or malformed, nothing is forwarded and the reason is
  logged.
- Forwarding never slows down or fails your app's exports. Exports wait in a
  4 MiB queue inside the instance and are retried up to three times on
  network errors, `429` and `5xx`; what does not fit or keeps failing is
  dropped. When your SDK retries an export that Gregale refused for its rate
  limit, the retry is not forwarded a second time.
- Forwarded traffic leaves from your app's instance and counts as its
  outbound traffic. Gregale's own rate limit and span caps do not apply to the
  forwarded copy.
- Forwarding needs `tracing.enabled`. Apps that configure their own exporter
  (below) are not forwarded, since their exports do not pass through Gregale.

## Sending metrics without an API key

Set `tracing.metrics` to have your app's OpenTelemetry metrics stored as its
custom metrics, with no API key or exporter configuration:

```yaml
tracing:
  enabled: true
  metrics: true
```

- Gregale points the metrics exporter at the in-instance receiver
  (`http://127.0.0.1:4318/v1/metrics`; gRPC on `127.0.0.1:4317` works too) and
  sets `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta` unless your
  app sets it, so the counters of several instances add up.
- Metrics are stored with the same rules and per-app name limit as the
  [OTLP metrics endpoint](scaling-policy.md#send-metrics-with-opentelemetry):
  you can chart them, alert on them and scale on gauges.
- The managed Node and Python auto-instrumentation also records metrics of
  its own (HTTP server and client durations, for example); each histogram
  uses five of the app's metric names. Points beyond the limit are dropped.
- Needs `tracing.enabled` and a plan with custom metrics. Without
  `tracing.metrics`, Gregale turns the SDK's metrics exporter off. An app that
  sets `OTEL_METRICS_EXPORTER` or `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` keeps
  its own choice.

## Using your own collector

If your app already sets `OTEL_EXPORTER_OTLP_ENDPOINT`,
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` or `OTEL_TRACES_EXPORTER`, Gregale leaves
your OpenTelemetry configuration alone, and a Python app that ships its own
`opentelemetry-sdk` is never instrumented a second time. Your existing exporter
keeps working; to send spans to the debugger as well, export to the public
`/v1/otel/v1/traces` endpoint with an API key.

Set `tracing.enabled: true` as well. It leaves your exporter alone, as above,
and it tells the debugger to keep a record of each traced request instead of
folding busy requests into one summary row: spans can only attach to a request
that has its own record, so without the setting most spans of a busy route
find no request and are dropped.

The key needs the `admin`, `deploy:write` or `metrics:write` scope; a
read-only key gets `403`. Spans attach to requests of the trace in apps the
key can write to:

- A key bound to an organization attaches spans to that organization's apps,
  whoever created them, while you can deploy there (owner, admin or
  developer).
- Any other key attaches spans to your own apps. Apps of an organization you
  have left, or can no longer deploy to, are skipped.

The endpoint answers `404` while tracing is not enabled for your account.

## Limits and behavior

- Available on plans that include the production debugger.
- Spans are kept with the request record for the plan's debugger retention.
- Tracing never stops your app from starting. If instrumentation cannot load,
  for example because of an incompatible library version, the app runs
  untraced.
- Instrumentation is loaded before Gregale snapshots your app, so waking from
  a snapshot adds no startup time; only a cold boot pays the load cost.
- Pre-fork servers such as gunicorn need OpenTelemetry's usual post-fork
  initialization in each worker.

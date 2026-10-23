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

The setting applies from the next deployment. Then open a request with
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
one query. When a dependency regresses, the evidence and the regression alert
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

## Following a request across services

`gregale trace <trace-id>` shows every retained request of one trace across
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
import "github.com/poyrazK/faas/sdk/go/tracing"

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

## Sampling

By default every request's spans are exported. To reduce volume, set a ratio:

```yaml
tracing:
  enabled: true
  sample_ratio: 0.25
```

Exports also count toward your plan's request telemetry rate limit; spans that
exceed it are dropped and the app keeps serving.

## Using your own collector

If your app already sets `OTEL_EXPORTER_OTLP_ENDPOINT`,
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` or `OTEL_TRACES_EXPORTER`, Gregale leaves
your OpenTelemetry configuration alone, and a Python app that ships its own
`opentelemetry-sdk` is never instrumented a second time. Your existing exporter
keeps working; to send spans to the debugger as well, export to the public
`/v1/otel/v1/traces` endpoint with an API key.

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

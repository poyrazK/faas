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

Other apps (Go, Dockerfile builds and container images) can export spans with
any OpenTelemetry SDK they already include. Inside the app, the standard
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

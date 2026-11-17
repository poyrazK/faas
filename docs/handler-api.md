# Handler API

Everything a Node or Python function handler receives and can use, on one
page. For deploying and configuring functions see [functions](functions.md);
for runtime details see [node24](runtimes/node24.md) and
[python313](runtimes/python313.md).

## Handler shapes

| | Node | Python |
|---|---|---|
| Recommended | `export default { async fetch(request, env, ctx) {} }` | `def handler(event, ctx)` or `async def handler(event, ctx)` |
| Also accepted | `export async function handler(event, ctx)` or `export default async function handler(event, ctx)` | |
| File | `handler.js` or `handler.mjs` | `handler.py` |

The Python runner always passes both arguments.

## The request

**Fetch handler (Node).** `request` is a standard `Request`. Its URL has
the origin `http://faas.local` plus the real path and query string. `env`
is a copy of the process environment.

**Event handler (Node and Python).** `event` holds:

| Field | Meaning |
|---|---|
| `method` | HTTP method; `POST` when the call has none (cron, queue) |
| `path` | request path, `/` by default |
| `query` | raw query string, without `?` |
| `headers` | request headers; lookups ignore case (`event.headers["x-api-key"]`); a repeated header arrives as one value joined with `, ` (`; ` for `Cookie`) |
| `body` | parsed JSON when the body is JSON, otherwise a string; `null` / `None` when empty |
| `body_b64` | the exact request bytes, base64-encoded; use it for signature checks |

## The context: `ctx`

| | Node | Python |
|---|---|---|
| Invocation ID | `ctx.invocation_id` | `ctx.invocation_id` |
| Cancellation | `ctx.signal` (an `AbortSignal`; a Fetch handler can also use `request.signal`) | `ctx.cancelled` (a `threading.Event`; an `async` handler's task is cancelled) |
| Logging | `ctx.log.debug/info/warn/error(...args)` | `ctx.log.info/debug/warning/error(*args, extra={...})` |
| Work after the response | `ctx.waitUntil(promise)` | — |
| Lifecycle hook | `ctx.lifecycle_hook`: `before_checkpoint`, `after_restore` or `""` | `ctx.lifecycle_hook` |

- Cancellation fires when the client goes away or the request runs out of
  time. Pass `ctx.signal` to `fetch`, database queries and other I/O.
- Every log line goes to `gregale logs`; so do `console.log` and `print`.
  Never log the request event: it can carry cookies and API keys.
- `ctx.waitUntil()` does not keep the instance alive. Work registered with
  it can be lost when the instance is replaced; send anything that must
  finish to a queue.

## The response

Return any of these:

| Return | Sent as |
|---|---|
| A `Response` (Node Fetch handler) | its status, headers and bytes |
| An object / dict with `body` | `status` or `statusCode` (default 200), `headers`, `body` |
| A string or bytes | the body, status 200 |
| Any other value | JSON |

A `body` that is not a string or bytes is JSON-encoded, and you set
`content-type` yourself. An object without a `body` key is sent whole as
JSON. A header value may be a list, one header line per item, so several
`Set-Cookie` headers work.

A thrown exception returns `500` with `{"error": "handler_error", "message",
"invocation_id"}`. It is not retried; a returned `4xx` is not retried
either.

**Streaming.** Return an async iterable or Node stream body (Node), or a
generator or async generator body (Python), and each piece reaches the
client as it is produced. A Fetch `Response` with a streaming body streams
too. See [Node](runtimes/node24.md#streaming) and
[Python](runtimes/python313.md#streaming).

## Platform headers

Gregale sets these headers and drops any value a client sends, so they can
be trusted:

| Header | Meaning |
|---|---|
| `x-faas-invocation-id` | the invocation ID, the same as `ctx.invocation_id` |
| `x-faas-deadline-ms` | milliseconds left in the request's budget at dispatch |
| `x-faas-invocation-source` | on platform calls only: what made the call, such as `cron`, `inbound_webhook`, `async_invoke` or `replay` |
| `x-faas-invocation-attempt` | on platform calls only: which delivery attempt this is |

Async, queue and cron calls are delivered at least once. Make them safe to
repeat with the invocation ID or a key of your own, not the attempt number.

## Time limits

A request must start its response within its request timeout, 30 seconds by
default. The whole call, a streamed response included, may run for the longer
of that timeout and 30 s. Raise it with `gregale app APP --request-timeout
SECONDS` or `request_timeout_s:` in `gregale.yaml`, up to 30 s on Free, 1 min
on Hobby, 5 min on Pro and 15 min on Scale.

Between requests the instance may pause, so timers and intervals do not run
then; await work that must finish before the request ends. An idle function
parks after 30 s by default and wakes on the next request
([scale-to-zero](cold-wake.md)).

## State between requests

Module-level code runs once per worker and is captured in the warm snapshot,
so create clients, pools and caches there, not in the handler. Requests can
share a worker (`concurrency:`, see
[functions](functions.md#concurrent-requests-per-worker)): keep per-request
data in local variables, never in globals. Durable state belongs in Postgres,
object storage or a queue.

Async setup belongs in an `init` hook, which the snapshot waits for, and
connections that must not be shared by restored instances are closed in
`beforeSnapshot` and reopened in `afterRestore`
([snapshot hooks](functions.md#snapshot-hooks)).

## Configuration and secrets

Environment variables and secrets are both read from the environment:
`process.env.NAME` (Node, or `env.NAME` in a Fetch handler) and
`os.environ["NAME"]` (Python). Set them with `gregale env set`, `env:` and
`secrets_from_env:` in `gregale.yaml`, or a secrets file at deploy
([environment variables](env.md), [secrets](secrets.md)). Secrets are sealed
at rest and never returned by the API.

## Databases, buckets and other functions

- **Postgres.** `gregale add postgres --app APP` injects `DATABASE_URL`. For a
  pool that respects cancellation and the warm snapshot, start from
  `gregale init --template rest-api-postgres` (see
  [database requests](functions.md#database-requests-and-cancellation)).
- **Object storage.** `gregale add bucket NAME --app APP` injects
  `<PREFIX>_ENDPOINT`, `_REGION`, `_BUCKET`, `_ACCESS_KEY_ID`,
  `_SECRET_ACCESS_KEY` and `_ADDRESSING_STYLE` for any S3 SDK
  ([object storage](object-storage.md#give-your-app-access)).
- **Other functions and services.** Call them over HTTP. With the SDK
  installed, a function call runs inside the SDK's request context with no
  wrapper: requests made through `createGregaleFetch` (Node,
  `@gregale/sdk-node`) or `GregaleReleaseTransport` /
  `AsyncGregaleReleaseTransport` (Python, `faas_sdk`) carry the time left and
  the release, so the callee stops when the caller runs out of time. A
  private function needs `Authorization: Bearer <API key>`. For work that
  does not need an answer now, send a queue message instead.

## Testing locally

Handlers are plain functions: call them from `node --test` or pytest with a
`Request` or an event dict. See
[Node](runtimes/node24.md#testing-locally) and
[Python](runtimes/python313.md#testing-locally). To run your code on Gregale
while you edit, use [`gregale dev`](gregale-dev.md).

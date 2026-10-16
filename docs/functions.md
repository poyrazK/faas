# Functions and runtimes

Functions are request handlers packaged with a supported runtime. Explicit
runtime and handler flags make the contract unambiguous:

```bash
gregale deploy --function --runtime node24 --handler handler.handler
gregale deploy --function --runtime python313 --handler handler.handler
gregale deploy --function --runtime binary
```

The current profiles include Node 22/24, Python 312/313, Go 124, and
[`binary`](runtimes/binary.md): one statically linked executable in any
compiled language (Rust, Zig, C, C++, Go), built from a Dockerfile at the
source root and served by the Go runner. Start from the `function-rust`
template. A handler returns one response or streams it: server-sent
events, generated text and large downloads reach the client as they are
produced ([Node](runtimes/node24.md#streaming),
[Python](runtimes/python313.md#streaming), [Go](runtimes/go124.md#streaming);
ADR-999). Gateway streaming is on for Hobby, Pro and Scale; Free buffers the
response. Every `Set-Cookie` header is sent. Node and Python handlers, and Go and `binary` handlers that
speak the [persistent protocol](runtimes/go124.md#persistent-handlers), stay
loaded between requests. Node and Python handlers are also captured warm in
the init snapshot; a Go or `binary` handler is restarted after a snapshot restore. For ordinary framework APIs, omit `--function`
and let source detection select the app profile.

A function request must start its response within 3 seconds by default.
For slower work, raise the function's request timeout:
`gregale app APP --request-timeout SECONDS`, up to 30 s on Free, 1 min on
Hobby, 5 min on Pro and 15 min on Scale (ADR-1150). The whole invocation,
a streamed response included, may run for the longer of that timeout and
30 s. The runner passes the time left to the handler in the
`X-Faas-Deadline-Ms` request header. In an event handler, `event.headers` has
lowercase names and ignores case on lookup (`event.headers["x-faas-deadline-ms"]`);
repeated request headers arrive joined into one value. Async, queue and cron invocations
of the function may run just as long: the longer of its request timeout
and 30 s (ADR-1180).

An idle function parks after 30 seconds by default on every plan; set
`gregale app APP --idle SECONDS` to keep it warm longer (see
[scale-to-zero](cold-wake.md#when-an-instance-parks)).

Between requests a function's VM may be paused: once nothing has been in
flight for 2 seconds, its vCPUs stop until the next request, which resumes
them in under a millisecond. Timers and intervals in your code do not run
while it is paused, as on AWS Lambda; finish background work with
`waitUntil()` ([Node](runtimes/node24.md)) before the request ends. At the
idle timeout a function may be [frozen](cold-wake.md#frozen-instances)
instead of parked. Set `lifecycle.pause` in `gregale.yaml` to `when_idle` to
keep it running between requests, or to `never` to turn off both.

Use `gregale invoke APP` for a smoke request and `gregale invocations get ID`
for an async result. Runtime-specific limits are in [plans](plans.md).

## Several functions in one project

List the functions in `gregale.yaml`. `gregale deploy` deploys each one as
its own function app, named `<project>-<name>`. A function either has its own
directory (`root:`) with a conventional `handler.js`, `handler.py` or Go
`main`, or names its file (`handler:`) in a source tree it shares with other
functions:

```yaml
functions:
  - name: thumbs
    root: functions/thumbs        # handler.js, handler.py, or a Go main
    runtime: node24               # optional; inferred from the handler file
    idle_timeout_s: 60            # optional
    bucket_events:
      - bucket: uploads           # bucket name or ID
        events: ["s3:ObjectCreated:*"]
        prefix: images/
  - name: payments
    root: functions/payments
    webhooks:
      - name: stripe
        provider: stripe          # stripe or generic
        signing_secret_env: STRIPE_WEBHOOK_SECRET
        delivery_path: /
  - name: digest
    root: functions/digest
triggers:
  - kind: cron
    app: digest                   # a function name
    schedule: "0 8 * * *"
    path: /
```

```bash
cd shop
gregale deploy            # deploys shop-thumbs, shop-payments, shop-digest
gregale deploy --dry-run  # previews each function without changing anything
```

The project part of each name is the name a plain deploy would use: `--name`,
or the directory's name. Functions deploy in the order listed. If one fails,
the rest are not attempted and the summary says which functions are live; run
the deploy again once it is fixed. Inside a monorepo workspace, a function's
build includes the workspace, so functions can share packages.

### Functions sharing one source tree

Node and Python functions can live side by side in one tree and share code
with ordinary imports. Name each function's file with `handler:`; `root:`
then defaults to the project directory:

```yaml
functions:
  - name: resize
    handler: api/resize.js        # imports ../lib/images.js
  - name: invoices
    handler: api/invoices.py      # imports helpers from api/
    runtime: python313            # optional; .js → node22, .py → python312
```

Each function uploads and builds the shared tree, and its handler file is
loaded where it lives, so relative imports keep working. A Node handler is a
`.js` or `.mjs` file and a Python handler a `.py` file, using the same
`handler(event, ctx)` or Fetch API exports as a single function. Go functions
are each built from their own `main`, so give every Go function its own
`root:`. Two functions may share a root only when their handlers differ.

After each function deploys, Gregale applies its manifest settings:

- **`idle_timeout_s`**: how long the function stays warm after a request.
- **Cron triggers**: the function's crons match `triggers:` exactly. Removing
  one from the file removes it.
- **`bucket_events`**: notifications from that bucket go to the function.
  Gregale changes only the notification rules it created for this function
  and leaves every other rule on the bucket alone. Configuring notifications
  needs the same permissions as `gregale bucket notifications set`. Use
  `app:` to manage the bucket through the app that holds its write grant.
- **`webhooks`**: an inbound webhook endpoint delivering to the function. The
  first deploy creates it from the signing secret in the named environment
  variable and prints the endpoint URL once; register that URL with the
  provider. Later deploys update the delivery path. Endpoints that are no
  longer listed are reported but never deleted, because deleting one would
  break the URL the provider calls.

Pass `--no-triggers` to deploy code without changing crons, bucket events or
webhooks. A function removed from the list keeps its app; the deploy names
it, and `gregale apps --yes <slug>` deletes it.

A manifest with `functions:` holds only `functions`, cron `triggers` and
`schema_version`. Other sections apply to a single app, so configure them per
function after deploying. `functions:` is applied by `gregale deploy`:
repository deploys connected through GitHub ignore it for a function app and
refuse it for any other app. The design is recorded in
[ADR-985](adr/985-multi-function-projects.md).

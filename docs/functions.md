# Functions and runtimes

Everything a handler receives and can use (request, `ctx`, responses,
platform headers, limits, secrets, databases) is on one page:
[handler API](handler-api.md).

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
[Python](runtimes/python313.md#streaming), [Go](runtimes/go124.md#streaming)). Gateway streaming is on for Hobby, Pro and Scale; Free buffers the
response. Every `Set-Cookie` header is sent. Node and Python handlers, and Go and `binary` handlers that
speak the [persistent protocol](runtimes/go124.md#persistent-handlers), stay
loaded between requests. Node and Python handlers are also captured warm in
the init snapshot; a Go or `binary` handler is restarted after a snapshot restore. For ordinary framework APIs, omit `--function`
and let source detection select the app profile.

A function request must start its response within 30 seconds by default.
Set a shorter timeout when callers need a fast failure, or a longer one for
slower work:
`gregale app APP --request-timeout SECONDS`, up to 30 s on Free, 1 min on
Hobby, 5 min on Pro and 15 min on Scale. The whole invocation,
a streamed response included, may run for the longer of that timeout and
30 s. The runner passes the time left to the handler in the
`X-Faas-Deadline-Ms` request header. In an event handler, `event.headers` has
lowercase names and ignores case on lookup (`event.headers["x-faas-deadline-ms"]`);
repeated request headers arrive joined into one value. Async, queue and cron invocations
of the function may run just as long: the longer of its request timeout
and 30 s.

A Node or Python function that loads the Gregale SDK runs each call inside
the SDK's request context, with no wrapper of its own. Service calls made
through `createGregaleFetch` (Node) or the SDK's HTTPX transports (Python)
then carry the time still left, the release and the signed chain deadline,
so a downstream service stops when this request runs out. A Go function gets
the same by serving `faas.GregaleReleaseMiddleware(handler)` and calling
services through an `http.Client` that uses `faas.NewGregaleReleaseTransport`.

An idle function parks after 30 seconds by default on every plan; set
`gregale app APP --idle SECONDS` to keep it warm longer (see
[scale-to-zero](cold-wake.md#when-an-instance-parks)).

Between requests a function's VM may be paused: once nothing has been in
flight for 2 seconds, its vCPUs stop until the next request, which resumes
them in under a millisecond. Timers and intervals in your code do not run
while it is paused, as on AWS Lambda; await work that must finish before
the request ends, or deliver it through a durable queue. At the
idle timeout a function may be [frozen](cold-wake.md#frozen-instances)
instead of parked. Set `lifecycle.pause` in `gregale.yaml` to `when_idle` to
keep it running between requests, or to `never` to turn off both.

`gregale deploy` ends with one line showing what the platform applies to the
function, defaults included, for example
`Runs with 256 MB · 30 s timeout · pauses between requests · parks after 30 s idle`.
The same values are in `effective_limits` on the app's API response
(`request_budget_ms`, `idle_timeout_s`, `pause`).

Use `gregale invoke APP` for a smoke request and `gregale invocation get ID`
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

### Config in code

A Node or Python handler can declare its own settings, so a new function is
one file. `gregale deploy` reads the config without running your code:

```js
// functions/digest/handler.js
export const config = { cron: "0 8 * * *", ramMb: 512 };

export default {
  async fetch(request) {
    return Response.json({ sent: true });
  },
};
```

```python
# functions/thumbs/handler.py
config = {"queue": "thumbs", "request_timeout_s": 60}

def handler(event, ctx):
    return {"status": 200, "body": {"ok": True}}
```

`gregale add function NAME --trigger http|cron|queue|bucket|webhook`
writes such a directory for you, with a handler, a test beside it and, for
Node, a `package.json`; `--lang python` picks Python. Nothing is created
remotely until you deploy:

```bash
gregale add function digest --trigger cron --schedule "0 8 * * *"
node --test functions/digest/handler.test.js
gregale deploy
```

With the SDK installed you can use the typed helpers instead:
`export const config = defineConfig({...})` from `@gregale/sdk-node`, or
`@function(cron="0 8 * * *")` from `faas_sdk` on `handler`.

- **Keys** are the `functions:` entry fields above, in snake_case or
  camelCase, plus two shorthands: `cron` (a schedule or list, called on
  path `/`) and `queue` (a queue name or list, pushed to the function).
  `name`, `root` and `handler` come from where the file is.
- **Literals only**: strings, numbers, booleans, null, lists and objects.
  A variable or `process.env.X` is an error naming the file and line; put
  values that change between deploys in `env` or secrets.
- **Discovery**: with no `functions:` key in `gregale.yaml` (or no
  `gregale.yaml`), every `functions/<name>/` directory whose `handler.js`,
  `handler.mjs` or `handler.py` declares a config deploys as function
  `<name>`. Directories without a config are left alone.
- **gregale.yaml wins**: for a function listed in `functions:`, each field
  the entry sets is kept and the code fills only the rest. Any cron trigger
  for the function in `triggers:` replaces the code's `cron`.
- `gregale deploy --plan` and every deploy list the settings that came from
  code. Pushes to a connected repository read config in code too where your
  account can [deploy functions from GitHub](#deploy-a-functions-project-from-github).

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
- **Realtime triggers**: a `kind: realtime` trigger runs the function for each
  message of a realtime consumer group (see
  [function triggers](realtime.md#function-triggers)). Once a function declares
  one, its realtime triggers match `triggers:` by `slug`; a changed endpoint,
  channel or group replaces the trigger. A function that declares none keeps
  realtime triggers created with `gregale trigger create`.
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
- **`queue_bindings`**: queues whose messages are pushed to the function,
  with the same fields as an app's `queue_bindings:` (`name`, `queue_name`,
  `max_concurrency`, `enabled`, `retry_policy`). A function consumes by
  push, so `mode` defaults to `push` and `workload_class` to `http`, as it
  does for a single function app's top-level `queue_bindings:` and for
  `gregale queue bindings create` on a function app. The
  function's bindings match the list by name: one you remove is deleted,
  and `queue_bindings: []` removes them all. Without the key, bindings
  made by hand are left alone.
- **`event_triggers`**: event subscriptions that deliver to the function,
  with the same fields as a top-level `event_triggers:` entry except `app:`.
  They are added and updated on each deploy but, as for any app, never
  removed by one.

```yaml
functions:
  - name: invoices
    root: functions/invoices
    queue_bindings:
      - name: jobs
        queue_name: invoice-jobs
        max_concurrency: 4
    event_triggers:
      - source: billing.*
        type: invoice.paid
```

### Per-function settings

A function can also set its app's resources and configuration. Gregale puts
them on the function's app before its deployment is built, creating the app
first when the function is new:

```yaml
functions:
  - name: users
    root: functions/users
    ram_mb: 512                   # within the plan's RAM per app
    request_timeout_s: 120        # within the plan's request ceiling
    min_instances: 1              # applied after the deploy (Pro and Scale)
    concurrency: 16               # requests one worker serves at once
    env:
      LOG_LEVEL: info
    secrets_from_env: [DATABASE_URL, STRIPE_KEY]
```

- An omitted key leaves the app's current setting alone.
- **`env`** sets plain values. Keys the manifest does not list are kept;
  remove one with `gregale env unset`, or set `prune_env: true` to make
  `env:` the function's whole env: each deploy then removes every other
  key and prints which. Secrets and environment-scoped values are never
  removed this way.
- **`secrets_from_env`** names secrets whose values are read from your
  environment when you deploy and sealed; the values never go in the file.
  A name you have not exported keeps the secret the app already has. A
  secret the app has never had stops that function before it deploys.
- **`concurrency`** is the function's `function_concurrency`; see
  [Concurrent requests](#concurrent-requests-per-worker).

### One address for a function set

Mark one function `entry: true` and give the others `routes:`, request path
globs. Requests to the entry function's address, and to its verified custom
domains, whose path matches a route go to that function; everything else
goes to the entry function:

```yaml
functions:
  - name: web
    root: functions/web
    entry: true                   # serves every path no route claims
  - name: users
    root: functions/users
    routes: ["/users", "/users/*"]
  - name: admin
    root: functions/admin
    routes: ["/users/admin/*"]    # the more specific route wins
```

`/users/*` matches anything below `/users/`; `/users/**` also matches
`/users` itself. Routes take effect once every function has deployed. Each is
a `kind=route` edge rule on the entry app named `functions:<function>`;
`gregale edge rule list --app <entry app>` shows them. Rules you add by hand keep the default
priority and are checked first; Gregale only changes and removes the rules it
named. Add a custom domain to the entry app and deploy again to route it too.

Pass `--no-triggers` to deploy code without changing crons, bucket events or
webhooks. A function removed from the list keeps its app; the deploy names
it, and `gregale app delete <slug> --yes` deletes it.

A manifest with `functions:` holds only `functions`, cron `triggers` and
`schema_version`. Other sections apply to a single app, so configure them per
function after deploying. `functions:` is applied by `gregale deploy`, and by
pushes to a connected repository where your account can
[deploy functions from GitHub](#deploy-a-functions-project-from-github);
otherwise a repository deploy ignores it for a function app and refuses it for
any other app.

### Deploy a functions project from GitHub

A functions project can deploy from its GitHub repository instead of a
checkout. This is rolling out: `gregale feature list` shows whether your
account has **Functions projects from repository pushes**
(`repository-functions`). Without it, use `gregale deploy` from a checkout.

Connect the repository through the Gregale GitHub App, then deploy it once as a
project:

```bash
gregale deploy --repo octo/shop --ref main --project
```

The project's production branch is `main` unless you pass
`--production-branch`. Gregale reads `gregale.yaml` and any `functions/<name>/` handlers at that
commit, shows the plan, and creates one function app per function with the
same `<project>-<name>` slugs `gregale deploy` uses. Function apps a checkout
deploy already created are adopted, not duplicated, and an adopted app does
not count against your app limit a second time. Only an app with the
project's owner is adopted: a company project never takes over a personal
app, or the other way round, and the deploy stops with a conflict on that slug.

After that, every push to the production branch:

- deploys each function whose root changed, and every function when the
  root `gregale.yaml` changed;
- creates an app for a function new to the repository;
- applies each function's settings as `gregale deploy` does: RAM, request and
  idle timeouts, minimum instances, concurrency, non-secret `env` (and
  `prune_env`), cron, queue bindings, event triggers and the entry function's
  routes, including config in code;
- keeps the app of a function the repository no longer declares. Its GitHub
  check says it was kept and names `gregale app delete <slug> --yes`.

A push to a branch mapped to another environment (staging, for example) sets
each function's `env` in that environment only, and `prune_env` removes only
that environment's variables. RAM, timeouts and concurrency belong to the
function in every environment, so only a production push changes them.
A push's env changes are one config revision attributed to the pusher
(`github:<login>`): `gregale env history` lists it and config rollback can undo
it, and every setting a push changes appears in the audit log and your
organization's activity. In a protected environment a push does not change
env; it fails for that function and asks for a reviewed `gregale deploy`.

A push runs unattended, so it leaves three steps for a person: creating an
inbound webhook endpoint (its URL is shown once), changing bucket
notifications (they need MFA), and reading `secrets_from_env` (the values come
from your shell, and Gregale never reads secrets from the repository).
Existing webhooks, bucket notifications and secrets stay as they are. The
push's deploy activity lists each step left, and running `gregale deploy` from
a checkout completes them; `gregale org activity` and the dashboard's
activity feed show it.

## Snapshot hooks

A parked function wakes from a snapshot of its workers, so anything they set
up at boot is copied into every instance restored from it. A Node or Python
function can declare three hooks in its handler file to manage that state:

- `init` runs in each worker after the module loads and before the worker is
  ready, on cold boot and before the snapshot is taken. The snapshot waits for
  it, so async setup (a warmed cache, a fetched config) is never captured half
  done.
- `beforeSnapshot` (Python `before_snapshot`) runs in every worker before a
  new snapshot is captured. Close connections and flush buffers here.
- `afterRestore` (Python `after_restore`) runs in every worker after a restore
  and when a [frozen instance](cold-wake.md#frozen-instances) resumes, before
  the instance serves traffic. Reopen connections and refresh values that must
  be unique per instance here.

```js
// handler.js
let db;
export default {
  async init() { db = await connect(process.env.DATABASE_URL); },
  async beforeSnapshot() { await db.end(); },
  async afterRestore() { db = await connect(process.env.DATABASE_URL); },
  async fetch(request) { return Response.json(await db.query("select 1")); },
};
```

```python
# handler.py
db = None

async def init(ctx):
    global db
    db = await connect(os.environ["DATABASE_URL"])

async def before_snapshot(ctx):
    await db.close()

async def after_restore(ctx):
    global db
    db = await connect(os.environ["DATABASE_URL"])

async def handler(event, ctx):
    return {"body": str(await db.fetchval("select 1"))}
```

In Node, hooks are methods on the default export or named exports beside
`handler`; each receives `{signal}`, an `AbortSignal` that fires if the call
is cancelled. In Python
they are module functions, sync or async, with an optional `ctx` argument.

`gregale deploy` finds the hooks in the handler file without running it (for a
[functions project](#several-functions-in-one-project), `gregale deploy --plan`
lists them under "Snapshot hooks in code:") and points the app's `before_checkpoint` and `after_restore` lifecycle at the function
runner, which calls the hook in every worker. A push to a [GitHub-connected
functions project](#deploy-a-functions-project-from-github) does the same. A
hook removed from the code is removed from the app on the next deploy. An
app that already has its own `before_checkpoint` or `after_restore` path
cannot also declare that hook in code; the deploy names the conflict.
Lifecycle settings are app-wide, so only a production deploy changes them.

The hooks have the [lifecycle hook contract](cold-wake.md#refresh-application-state-after-restore):
`beforeSnapshot` and `afterRestore` have 500 ms each. In a functions project,
an entry (or its config in code) can give them up to 2 seconds with
`before_snapshot_timeout_ms` and `after_restore_timeout_ms`
(`beforeSnapshotTimeoutMs` and `afterRestoreTimeoutMs` in Node,
`before_snapshot_timeout_ms` in Python's `@function`). Unlike other entry
keys, an omitted timeout means the 500 ms default, and setting one for a hook
the handler does not declare is refused. A failing `afterRestore`
rejects the restored instance and Gregale cold-boots a fresh one. A failing
`beforeSnapshot` on the first capture fails the deployment with
`before_checkpoint_failed`, and declaring it turns off warm-tier snapshot
captures for the function. A throwing `init` fails the deployment with
`function_init_failed` and its error ([errors](errors.md)); an `init` that
does not finish within the startup deadline fails it with
`app_startup_timeout`.

## Concurrent requests per worker

A Node or Python function runs your handler in up to four worker processes
per instance, each serving one request at a time. A handler that mostly
waits on a database or another service can serve many requests from one
worker instead:

```bash
gregale app shop-users --function-concurrency 16
gregale deploy            # the setting applies from the next deploy
```

or `concurrency: 16` in a `functions:` entry. Each worker then takes up to
16 requests at once, and a second worker starts only when the first is full.
The value can be up to your plan's per-instance concurrency (`gregale app
<slug> --concurrency`: Free 4, Hobby 5, Pro 25, Scale 80).

- Requests on one worker share its module state. Keep per-request data in
  local variables, not globals.
- Node runs any handler this way. CPU-heavy handlers block the event loop
  for every request on the worker, so leave them at 1.
- Python runs an `async def handler` as one asyncio task per request, and a
  plain `def handler` on one thread per request. Threads share Python's
  global interpreter lock, so this helps handlers that wait, not ones that
  compute.
- A request whose client goes away or that runs out of time is cancelled
  cooperatively. Node aborts `ctx.signal` and a Fetch handler's
  `request.signal`; pass the signal to downstream I/O. Python cancels an
  async handler's task. A sync handler may poll `ctx.cancelled.is_set()`;
  its thread cannot be forcibly interrupted. Cancelled output is suppressed.
  The slot stays occupied through handler and stream cleanup, including
  async `finally` blocks. A handler that cannot finish cancellation within
  five seconds causes its worker to restart, failing its other in-flight
  requests too.
- Go SDK functions keep one request per process. A `binary` function's
  handler can serve several by advertising them in its ready line (see the
  [`binary` runtime](runtimes/binary.md)).


### Database requests and cancellation

Keep one database adapter at module scope and pass each request's own signal.
`gregale init --template rest-api-postgres` provides `database.js` and its
vendored `runtime/` (the SDK's `PostgresPoolBinding` and secret coordinator):

```js
import pg from 'pg';
import {createDatabase, poolConfig} from './database.js';

const db = createDatabase(
  config => new pg.Pool({...poolConfig(config.connectionString), max: 4}),
  {maxInFlight: 4, readinessQuery: 'SELECT 1'},
);
const started = db.start();

export async function handler(event, ctx) {
  await started;
  const result = await db.query('SELECT id FROM notes LIMIT 20', [], {signal: ctx.signal});
  return {body: result.rows};
}
```

The SQL connection limit and `maxInFlight` apply per worker process. Budget
for up to four workers per VM and all live instances. `database_busy` means
the local admission bound was reached. Cancellation closes the checked-out
connection; PostgreSQL may keep executing until it notices the disconnect or
the statement deadline. An uncommitted single-query transaction rolls back,
but cancellation cannot undo an already committed write. The adapter never
replays SQL; use application idempotency when retrying an uncertain COMMIT.

Configured function lifecycle endpoints can inspect `ctx.lifecycle_hook`,
which is `before_checkpoint`, `after_restore` or an empty string. The runner
derives it from guest-init's loopback socket and marker; event headers and
request bodies cannot set it. Match both this context and your configured
hook path before calling `db.checkpoint({signal: ctx.signal})` or
`db.restore({signal: ctx.signal})`. Production function hooks run in one
worker, so they cannot prove that pools in every worker have drained. The
single HTTP-process PostgreSQL starter remains the reference for production
checkpoint pool ownership.

The completion fence covers the handler and its response stream: await work
that must finish with the invocation. `ctx.waitUntil()` does not extend an
instance's lifetime, so work after the response can be lost when a worker or
VM is replaced. Use a durable queue for work that must survive replacement.
Automatic pool ownership needs the current SDK runtime or a regenerated bundled
helper; direct `pg.Pool` instances and older helpers are not registered.

# MCP on Gregale

This starter has four harmless tools, a public welcome resource, a customer
record URI template, and a summarize prompt with argument completions. It uses stateless Streamable HTTP,
request-scoped SSE progress, cancellation and optional external OAuth JWT
authentication. It supports MCP 2026-07-28 and stateless compatibility with
2025-11-25.

```
npm ci
npm test
npm start
gregale mcp doctor --url http://127.0.0.1:8080/mcp --legacy
gregale mcp call --url http://127.0.0.1:8080/mcp --tool add --arguments '{"a":7,"b":5}'
gregale mcp complete --url http://127.0.0.1:8080/mcp --prompt summarize --argument style --value exec
gregale mcp deploy --path . --name my-mcp --profile small
gregale mcp config --url https://my-mcp.gregale.dev/mcp --name my-mcp
```

`gregale-mcp.json` declares the MCP contract. `gregale.yaml` declares ordinary
HTTP hosting. The deploy command uploads the worktree, requires a streaming-enabled
plan, stages a zero-traffic candidate, verifies its preview and promotes with a
serving-revision guard. Failed checks preserve the serving revision. Use
`--release-policy` with reviewed role baselines to gate catalog changes. An empty
`allowed_origins` rejects every browser origin; non-browser MCP clients normally
omit Origin. Add exact trusted origins when a browser client needs access.

The starter explicitly uses public access. Before adding sensitive tools, set:

```json
"auth": {
  "mode": "external-oauth",
  "issuer": "https://identity.example.com",
  "jwks_url": "https://identity.example.com/.well-known/jwks.json",
  "resource": "https://my-mcp.gregale.dev/mcp",
  "scopes": ["mcp:tools"],
  "tool_scopes": {
    "greet": [],
    "add": ["math:read"],
    "stream_demo": ["mcp:stream"],
    "build_report": ["reports:build"]
  },
  "resource_scopes": {
    "greeting://welcome": [],
    "customer://records/{recordId}": ["records:read"],
    "task://tasks/{taskId}": ["reports:read"]
  },
  "prompt_scopes": {
    "summarize": ["reports:read"]
  }
}
```

Configure the provider for OAuth 2.1, PKCE, authorization-server discovery and the
canonical MCP resource audience. The resource server verifies RS256/ES256 JWT
access tokens with an expiry and subject, issuer, audience and all required
scopes. Opaque tokens need a provider-specific introspection adapter. The provider
owns registration, login and consent. `auth.scopes` cover the endpoint;
`tool_scopes`, `resource_scopes` and `prompt_scopes` add scopes for individual
tools, resource URIs/URI templates and prompt names. An empty scope array permits
every endpoint caller, while a configured map denies unlisted entries. Open mode
accepts only empty scope arrays. Null policies/arrays fail validation. Omitting a
map retains endpoint-only compatibility for that catalog type.

Register tools, resources and prompts through the helpers in `app.js`, and add
their policies to `gregale-mcp.json`. Each request gets a fresh catalog filtered
by verified scopes. Calls, reads and prompt gets are checked before dispatch and
again in the callback using the SDK's verified request context. Insufficient
scopes produce a 403 bearer challenge naming the required scopes. Client headers,
arguments and annotations cannot grant permission. Check ownership of any
tenant/object inside the callback using verified identity
(`ctx.http.authInfo.extra.subject`), never an unverified tenant ID from arguments.
This is application policy, so custom servers must implement their own
enforcement.

## Prompt and resource completions

The `summarize` prompt completes its optional `style` argument with `brief`,
`technical` and `executive`. The customer-record template completes only the
three harmless sample IDs included in the starter. Try either through the CLI:

```sh
gregale mcp complete --url http://127.0.0.1:8080/mcp \
  --prompt summarize --argument style --value exec
gregale mcp complete --url http://127.0.0.1:8080/mcp \
  --resource-template 'customer://records/{recordId}' --argument recordId --value example-
```

Use `--context` or `--context-file` to pass other already-resolved string
arguments for dependent suggestions. MCP completions are not tool calls. Keep
suggestions bounded and caller-scoped; never return customer IDs or other private
values unless the current caller is authorized to see them.

Use a client token via `--token-env MCP_TOKEN`; Gregale CLI account credentials are
never forwarded to this server. Capture `mcp lock` baselines with the same identity
and scopes when comparing a candidate server.

Keep durable state outside the VM. Avoid fetching customer credentials before
checkpointing; fetch short-lived credentials during a verified tool request.
Tool logs contain only name, duration and outcome. Calls are not automatically
retried. Doctor lists tools without executing them. An explicit `--stream-tool
stream_demo` doctor probe runs that tool and verifies spaced progress events.

## Durable MCP Tasks

The `build_report` sample and app-registered handlers can return a durable MCP
Task handle instead of blocking the `tools/call` response. Tasks are disabled by default. To enable
them, attach a read/write PostgreSQL binding to the app, keep a stable
`MCP_TASK_OWNER_KEY` secret of at least 32 bytes, and set `tasks.enabled` in
`gregale-mcp.json`:

```json
"tasks": {
  "enabled": true,
  "database_url_env": "DATABASE_URL",
  "owner_key_env": "MCP_TASK_OWNER_KEY",
  "owner_key_id": "initial",
  "payload_encryption_version": 1,
  "previous_owner_keys": [],
  "namespace_env": "FAAS_APP_ID",
  "ttl_seconds": 86400,
  "poll_interval_ms": 2000,
  "worker_concurrency": 1
}
```

The starter creates and migrates its namespaced task table, queue index,
notification function, and update trigger in that database. The bound role
must be able to create and alter the table, index, function, and trigger. The
store hashes owner identity and encrypts arguments, results, and errors with
AES-256-GCM before writing them.
The default payload format preserves the current task encryption wire format.
Configure the same active and previous keys on every serving replica and task
worker that shares the PostgreSQL database.
For rotation, first deploy a rotation-capable starter everywhere, distribute
the next key as a previous key while the current key remains active, then
switch the active key and keep the previous key configured. After that rollout
completes, set `payload_encryption_version` to `2`; live payloads are rewrapped
in bounded background batches. Keep the old owner key configured for at least
30 days after the active-key rollout completes because existing caller
ownership hashes cannot be recomputed. Tasks expire after the configured TTL
(60 seconds to 30 days).
`MCP_TASK_NAMESPACE` overrides
`FAAS_APP_ID`; use the same stable value in every app that shares the task
database.

Try the built-in task with:

```sh
gregale mcp call --url http://127.0.0.1:8080/mcp --tasks \
  --tool build_report --arguments '{"report":"weekly","steps":8}'
gregale mcp call --url http://127.0.0.1:8080/mcp --tasks \
  --tool build_report --arguments '{"report":"weekly","steps":8,"request_approval":true}'
gregale mcp task-wait --url http://127.0.0.1:8080/mcp --task-id TASK_ID
gregale mcp resource-read --url http://127.0.0.1:8080/mcp --uri task://tasks/TASK_ID
gregale mcp resource-watch --url http://127.0.0.1:8080/mcp --uri task://tasks/TASK_ID
```

The server only creates a handle after PostgreSQL confirms the row, so
`tasks/get` can read it immediately. By default, the HTTP server also claims
tasks for backwards-compatible single-process operation. To run a separate
Gregale worker, use the same source with a worker command and the same database,
owner key, and explicit namespace:

When Tasks are enabled, `task://tasks/{taskId}` is also a caller-scoped resource
containing the task's current status and result. Its reads require ownership of
the task and permission for the originating tool, in addition to the resource
scope. `mcp resource-watch` can subscribe to the URI and re-read it whenever
the task changes. Call `notifyResourceUpdated(uri)` on the object returned by
`createApp` after changing an application-owned resource. The default update
adapter is process-local; pass a shared `resourceUpdateAdapter` with
`publish(uri)` and `subscribe(uri, listener)` when resources can change on
another web replica. PostgreSQL `LISTEN`/`NOTIFY` carries task changes to web
replicas; bounded polling remains the recovery path if a database listener is
unavailable.

```sh
# Set this in the HTTP app so it accepts tasks without executing them.
MCP_TASKS_ROLE=web

# Use this command for the dedicated worker app.
npm run start:tasks-worker
```

For the worker app, set its `hosting.start` to `npm run start:tasks-worker` and
add a `worker` manifest block with a drain timeout suitable for your longest
handler. For example:

```yaml
hosting:
  start: npm run start:tasks-worker
worker:
  drain_timeout: 45s
  stop_signal: SIGTERM
  scale:
    min: 1
    max: 10
    metric: custom
    name: mcp_tasks_outstanding
    target: 4 # match tasks.worker_concurrency
```

Deploy the worker as a separate Gregale app. Bind the same PostgreSQL database
and `MCP_TASK_OWNER_KEY` to both apps, and set the same `MCP_TASK_NAMESPACE` in
both: Gregale assigns different app IDs to the web and worker apps. The worker
has no HTTP listener. On `SIGTERM` it stops taking new tasks, lets active
handlers finish within the platform drain window, then closes its database
pool. If it is killed before a handler finishes, the task lease expires and
another worker may replay it.

To scale this dedicated worker from its PostgreSQL task backlog, set
`MCP_TASKS_SCALING_APP_SLUG` to the worker app's Gregale slug and provide
`MCP_TASKS_SCALING_TOKEN` as a secret containing an API key restricted to
`metrics:write`. `GREGALE_API_URL` optionally selects a non-production API;
otherwise the publisher uses `https://api.gregale.dev`. Each worker publishes
the namespace's aggregate outstanding-task count as `mcp_tasks_outstanding`,
oldest outstanding-task age as `mcp_tasks_oldest_age_seconds`, and claimable
tasks without a live compatible handler as
`mcp_tasks_without_compatible_worker` every 15 seconds. Compatibility uses the
exact tool name and handler version with a worker heartbeat fresh within 45
seconds. The count excludes exhausted retries and live leases. These gauges
contain no task or caller identifiers. Gregale scales to
`ceil(outstanding / target)`, so set `worker.scale.target` to the worker's
`tasks.worker_concurrency`. Keep `worker.scale.min` at 1 while using this
in-process publisher; with zero workers, no process remains to refresh the
custom metric. Custom metrics require Hobby or higher. An empty pool starts at
its configured minimum so the publisher can send an initial reading; once a
worker exists, missing or stale metrics preserve the current pool rather than
treating unknown database state as an empty queue. Cancellation is cooperative.
Handlers that cause external side effects must be idempotent using the task ID.
The sample only computes a report string and is safe to replay.
The optional `request_approval` argument demonstrates an elicitation form
request; clients that use it must declare form elicitation support. New sample
tasks use handler version `2`, while this starter keeps a version `1` handler
for tasks already in the shared queue.

Register application handlers in `task-handlers.js`. `task-runtime.js` imports
that module in both the web and dedicated worker processes. Register the
matching MCP tool with `createApp().registerTool(...)`; clients that advertise
MCP Tasks are queued automatically for registered task handlers, while other
clients use the regular tool callback. The registered handler's `validate`
function checks arguments before they are persisted.

```js
registerMcpTaskHandler('lookup', {
  version: '1',
  validate(args) {
    if (typeof args?.key !== 'string' || !args.key.trim()) throw new Error('key is required');
  },
  async execute(args, { taskId, signal }) {
    const value = await lookupRecord(args.key, { idempotencyKey: taskId, signal });
    return { content: [{ type: 'text', text: value }] };
  },
});
```

Retain earlier implementations under `previousVersions` until their queued
tasks drain. A worker only claims the versions registered in its process. Keep
the handler registry identical across every app sharing the task namespace.

Custom task handlers can pause for client input through the execution context:

```js
async execute(args, { taskId, requestInput, requestInputs }) {
  const approval = await requestInput('approval', {
    method: 'elicitation/create',
    params: { mode: 'form', message: 'Approve this operation?', requestedSchema: { type: 'object', properties: { approved: { type: 'boolean' } } } },
  });
  // Continue using approval.content.approved.
}
```

Use `requestInputs({ key: request })` to publish several prompts together.
Request keys are stable and unique for the full task lifetime. The task store
encrypts both requests and responses; `tasks/get` exposes only unanswered
requests and `tasks/update` resumes execution after every outstanding request
has a response. A resumed handler starts again from its beginning, so it must
use the task ID for idempotency and replay the same request key and payload to
read the saved response. The starter accepts elicitation, sampling, or roots
requests only when the original client declared the corresponding capability.

Modern clients can also open `subscriptions/listen` with
`notifications.taskIds` to receive an initial full task snapshot and later
`notifications/tasks` updates on the same SSE stream. Resource subscriptions
can watch any registered URI the caller is authorized to read. The starter
checks task ownership and the tool policy before acknowledging a task resource.
Reopening a task subscription returns current snapshots, so clients can recover
after a dropped connection; clients can continue using `tasks/get` polling when
notifications are unavailable. PostgreSQL `LISTEN`/`NOTIFY` carries updates
from a separate worker to the web process; bounded polling takes over if the
database connection or pooler cannot keep a listener open.

The portable suite exercises the MCP wire with a memory store and checks SQL
parameters through a fake PostgreSQL pool. The repository's PostgreSQL 16
integration job also runs the store against a real database, covering table
creation, payload encryption, owner/app isolation, concurrent worker claims,
lease recovery, cancellation, retry exhaustion, expiry cleanup, and task
recovery by a replacement worker after a process crash, including encrypted
input requests, partial responses, and `tasks/update` resume. To run it
locally, point `MCP_TASKS_TEST_DATABASE_URL` at a disposable PostgreSQL database
and run `npm run test:postgres`. The test creates and removes its own schema.

Single-process mode can scale to zero, in which case the next task request wakes
the app and queued work resumes. A separate worker is a Gregale worker app. Keep
at least one worker replica when it publishes its own scaling metrics; to scale
the worker to zero, run the separate read-only tasks observer described below.
Do not store access tokens in task arguments; the store already keeps payloads
encrypted and short-lived, but application handlers still own data-minimization
and authorization.

Task admission defaults to 1000 outstanding tasks per namespace and 100 per
owner; override `tasks.max_outstanding` and `tasks.max_outstanding_per_owner`.
Queued, running and input-required tasks count against admission atomically.
Versioned workers claim only supported handlers; retain earlier implementations
with `previousVersions: { '1': executeV1 }` or keep the old worker until it drains.

`npm run start:tasks-observer` publishes aggregate queue metrics from a separate
always-running process, allowing worker replicas to start from zero. Give it a
read-only database credential with `SELECT` on both `gregale_mcp_tasks` and
`gregale_mcp_task_worker_handlers`, the shared namespace and worker scaling metrics
credentials. Set `MCP_TASKS_SCALING_APP_SLUG` to the worker slug you pass as
`--app`. It needs no owner key and never migrates schema or claims tasks.
Alongside the queue gauges, only this observer role publishes
`mcp_tasks_observer_heartbeat`; a worker publisher cannot keep that signal fresh.
It also publishes a bounded handler-compatibility snapshot with waiting task and
live worker counts by tool/version. `gregale mcp tasks status --details` reads
that snapshot from the API; it needs no local task database credentials.
The observer must stay running while the worker fleet is at zero; metrics
published by worker processes themselves require at least one worker replica.
At startup and before each queue read, the observer checks that its effective
database role cannot write or truncate either task table, create objects in its
schema, or create schemas in the database. A failed check prevents the heartbeat
and emits the redacted `mcp_task_observer_health_check_failed` log event. Alert
on that event and the payload-free `mcp_task_metrics_publish_failed` event.

Configure the dedicated worker's task-backlog scaler after deploying the worker:

```sh
gregale mcp tasks setup --app mcp-worker
gregale mcp tasks observer setup --app mcp-observer --worker-app mcp-worker --config ./gregale-mcp.json
gregale mcp tasks observer setup --app mcp-observer --worker-app mcp-worker --config ./gregale-mcp.json --apply
gregale mcp tasks observer status --app mcp-observer --worker-app mcp-worker --config ./gregale-mcp.json
gregale mcp tasks setup --app mcp-worker --observer-app mcp-observer --min 0 --apply
gregale mcp tasks status --app mcp-worker
```

The first command previews the worker policy. Observer setup checks the task
config, process entrypoint, matching namespace and metrics target, and existing
secret bindings; `--apply` raises the observer's worker minimum to one. It does
not create the app or provision secrets. Observer status waits for a running
observer and fresh heartbeat before you link the worker. Confirm that its
database role is read-only and its API token is restricted to `metrics:write`;
the CLI cannot inspect either permission. At runtime, the observer verifies its
effective PostgreSQL grants before publishing queue metrics or its heartbeat;
an unsafe grant stops heartbeat publication and logs a redacted health event.
Worker `status` reports the linked
app readiness and heartbeat, and `--require-fresh` checks both alongside the
task gauges. The scheduler holds the current worker count when the linked
observer or heartbeat is unavailable.

To notify when queued work gets too old, create a webhook alert on
`mcp_tasks_oldest_age_seconds`; the threshold is seconds (for example,
`--comparison gt --threshold 300`). Missing or stale readings mark the rule
degraded, and this signal cannot trigger deployment actions. See the MCP docs
for the complete `gregale alerts add` command.

To notify if a required task metric stops arriving, add a webhook rule on
`mcp_tasks_metrics_stale` with `--comparison gt --threshold 0`. It fires when
the outstanding-task, oldest-age, or no-compatible-worker gauge is missing,
stale, or invalid. For a
min=0 worker, it also requires a fresh observer heartbeat with value 1.

For an additional gateway JWT/scope gate, run `gregale mcp policy --path . --name
my-mcp` after configuring external OAuth. This separately updates the app-wide
JWT edge rule. The provider retains login and token issuance; keep application
catalog filtering and owner checks. Gateway resource policies accept simple
`{variable}` templates. Native deployment, restore and real provider/client login
qualification must be completed before claiming production support.

## Runtime catalog changes

`createApp` returns process-local catalog operations. Register entries before
starting the HTTP listener, or call them from application code while it runs:

```js
import * as z from 'zod/v4';

const mcp = createApp(config, { taskRuntime });
mcp.registerTool('lookup', {
  description: 'Find a record by key.',
  inputSchema: z.object({ key: z.string().min(1) }),
}, async ({ key }) => ({
  content: [{ type: 'text', text: await lookupRecord(key) }],
}));
const { app } = mcp;
```

The returned object also provides `replaceTool`/`removeTool`,
`registerResource`/`replaceResource`/`removeResource`, and
`registerPrompt`/`replacePrompt`/`removePrompt`. Resource registration uses the
same `(name, uriOrTemplate, definition, handler)` arguments as the MCP SDK.
Handlers must validate their own tool arguments and enforce ownership for
application data. Successful catalog changes notify active subscribers when
that caller's authorized catalog changed. In external OAuth mode, add every
dynamic name or resource URI pattern to the matching scope allow-list; missing
policy entries stay hidden and denied. The registry is local to one process, so
every serving replica needs the same application registration code.

`gregale mcp deploy` reads this digest from its zero-traffic candidate and pins
the promoted deployment revision with `MCP_CATALOG_EXPECTED_SHA256`. For other
deploy paths, set that variable to the `/healthz` digest on every serving
replica. Remove any existing app-wide env or secret with this key before using
`gregale mcp deploy`; those runtime layers take precedence over deployment
overrides. A mismatch makes `/healthz` return 503 and blocks MCP requests.
The health response includes Gregale's `FAAS_INSTANCE_ID`. In addition,
`gregale mcp status` receives the selected replica identity from Gregale's
authenticated exact-instance probes. It initializes MCP and lists the
caller-visible catalogs on every running replica without invoking tools,
reading resources, or relying on load-balancer sampling. Use `--token-env` or
`--release-policy` for external OAuth endpoints. The
digest covers advertised definitions, OAuth per-entry allow-lists, and whether Tasks are
enabled, not handler implementation; deploy the same reviewed build to all
replicas.

## Graceful shutdown

On SIGTERM/SIGINT the runtime stops accepting new work and drains active tasks.
Set `MCP_DRAIN_TIMEOUT_SECONDS` (default 10; range 1–300) below the worker's
`drain_timeout`, with at least 10 seconds left for cleanup. At the deadline,
unfinished tasks are returned to the queue with their old lease revoked;
requested cancellations remain cancelled. Observe the handler cancellation
signal and make external side effects idempotent using the task ID. HTTP
connections also have a bounded drain window. Queue recovery falls back to lease
expiry if the database is unavailable or the process is killed before cleanup.

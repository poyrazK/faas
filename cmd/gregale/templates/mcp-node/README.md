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

The `build_report` sample can return a durable MCP Task handle instead of
blocking the `tools/call` response. Tasks are disabled by default. To enable
them, attach a read/write PostgreSQL binding to the app, keep a stable
`MCP_TASK_OWNER_KEY` secret of at least 32 bytes, and set `tasks.enabled` in
`gregale-mcp.json`:

```json
"tasks": {
  "enabled": true,
  "database_url_env": "DATABASE_URL",
  "owner_key_env": "MCP_TASK_OWNER_KEY",
  "namespace_env": "FAAS_APP_ID",
  "ttl_seconds": 86400,
  "poll_interval_ms": 2000,
  "worker_concurrency": 1
}
```

The starter creates and migrates its namespaced task table, queue and fairness
indexes, owner-cursor table, claim-order sequence, notification and fairness
functions, and their triggers in that database. The bound role must be able to
create and alter tables, indexes, sequences, functions, and triggers. The store
hashes owner identity and encrypts arguments, results, and errors with AES-256-GCM
before writing them. Workers rotate among active owner partitions and preserve
FIFO order within each owner; open-mode callers share one partition.
The key must remain stable while tasks exist;
rotating it early makes those records unreadable. Tasks expire after the
configured TTL (60 seconds to 30 days). `MCP_TASK_NAMESPACE` overrides
`FAAS_APP_ID`; use the same stable value in every app that shares the task
database.

Try the built-in task with:

```sh
gregale mcp call --url http://127.0.0.1:8080/mcp --tasks \
  --tool build_report --arguments '{"report":"weekly","steps":8}'
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
the task changes. PostgreSQL `LISTEN`/`NOTIFY` carries task changes to web
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
the namespace's aggregate outstanding-task count as `mcp_tasks_outstanding` and
oldest outstanding-task age as `mcp_tasks_oldest_age_seconds` every 15 seconds.
These gauges contain no task or caller identifiers. Gregale scales to
`ceil(outstanding / target)`, so set `worker.scale.target` to the worker's
`tasks.worker_concurrency`. Keep `worker.scale.min` at 1 while using this
in-process publisher; with zero workers, no process remains to refresh the
custom metric. Custom metrics require Hobby or higher. An empty pool starts at
its configured minimum so the publisher can send an initial reading; once a
worker exists, missing or stale metrics preserve the current pool rather than
treating unknown database state as an empty queue. Cancellation is cooperative.
Handlers that cause external side effects must be idempotent using the task ID.
The sample only computes a report string and is safe to replay.

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
`notifications/tasks` updates on the same SSE stream. The starter checks task
ownership and the tool policy before acknowledging each ID. Reopening the
subscription returns current snapshots, so clients can recover after a dropped
connection; clients can continue using `tasks/get` polling when notifications
are unavailable. PostgreSQL `LISTEN`/`NOTIFY` carries updates from a separate
worker to the web process; bounded polling takes over if the database
connection or pooler cannot keep a listener open.

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
the app and queued work resumes. A separate worker is a Gregale worker app and
should have at least one replica for prompt execution. Do not store access
tokens in task arguments; the store already keeps payloads encrypted and
short-lived, but application handlers still own data-minimization and
authorization.

Task admission defaults to 1000 outstanding tasks per namespace and 100 per
owner; override `tasks.max_outstanding` and `tasks.max_outstanding_per_owner`.
Queued, running and input-required tasks count against admission atomically.
Versioned workers claim only supported handlers; retain earlier implementations
with `previousVersions: { '1': executeV1 }` or keep the old worker until it drains.

`npm run start:tasks-observer` publishes aggregate queue metrics from a separate
always-running process, allowing worker replicas to start from zero. Give it a
read-only database credential, the shared namespace and worker scaling metrics
credentials. It needs no owner key and never migrates schema or claims tasks.
Worker-only metrics still require at least one worker replica. Alert on the
payload-free `mcp_task_metrics_publish_failed` log event.

Configure the dedicated worker's task-backlog scaler after deploying the worker:

```sh
gregale mcp tasks setup --app mcp-worker
gregale mcp tasks setup --app mcp-worker --min 0 --apply
gregale mcp tasks status --app mcp-worker
```

The first command previews the policy. Apply `--min 0` only after the separate
observer is running; the CLI does not create the observer or provision its
database and metrics credentials. `status` shows whether the worker's task
metrics are present and fresh.

For an additional gateway JWT/scope gate, run `gregale mcp policy --path . --name
my-mcp` after configuring external OAuth. This separately updates the app-wide
JWT edge rule. The provider retains login and token issuance; keep application
catalog filtering and owner checks. Gateway resource policies accept simple
`{variable}` templates. Native deployment, restore and real provider/client login
qualification must be completed before claiming production support.


### Running Task capacity

`tasks.max_running` (default 16) limits live execution leases across all worker
replicas in a namespace. `tasks.max_running_per_owner` (default 4) limits each
owner within that namespace and must not exceed the namespace limit. These are
separate from `worker_concurrency`, which limits each worker process, and from
outstanding Task admission limits. PostgreSQL serializes capacity checks and
claims, while owner rotation skips owners whose execution capacity is full.

Use the same limits on every worker and metrics observer in a namespace, and
upgrade every worker before relying on enforcement. Completion, failure, and
input pauses release execution capacity. Running cancellation releases capacity
when acknowledged or when its lease expires. Expired Task TTLs and leases stop
counting automatically; expired leases cannot be renewed. Lease expiration does
not guarantee that an old handler has physically stopped, so handlers must still
make external side effects idempotent.

The `mcp_tasks_running` gauge counts unexpired live leases.
`mcp_tasks_capacity_waiting` counts unexpired queued Tasks and expired-lease
running Tasks blocked by namespace or owner capacity, regardless of handler
availability or retry eligibility. A capacity limit can explain backlog even
when more replicas would not help. Both gauges use the configured observer limits
and contain no customer identifiers.


### Task retry policy and failure metrics

Handler errors remain terminal by default. Opt into retries only for failures
that are safe to repeat:

```js
import { RetryableMcpTaskError } from './tasks.js';
// Inside a Task handler, after classifying a transient provider failure:
throw new RetryableMcpTaskError('Provider temporarily unavailable');
```

Configure `tasks.max_attempts` (default 3, range 1–10, including the initial
execution), `tasks.retry_base_delay_ms` (default 1000), and
`tasks.retry_max_delay_ms` (default 60000). Delay bounds must be integer
milliseconds between 100 and 86400000, and the maximum must be at least the
base. Each retry waits a random delay between half and all of
`min(maximum, base * 2^(attempt - 1))`. PostgreSQL persists the due time; retries
survive restarts, retain the same Task ID and encrypted arguments, and release
running capacity during the wait. Delayed retries still count against outstanding
admission limits. Cancellation wins over retry scheduling. Exhausted attempts or
a delay that would reach the Task TTL produce a terminal failure. Retries never
extend TTL. Input pauses do not consume another execution attempt.

Keep retry settings consistent across every worker in a namespace. Upgrade all
workers before enabling retryable handler errors, since older workers do not
respect delayed retry scheduling. Handlers still need idempotent external side
effects keyed by Task ID; retry classification does not provide exactly-once
execution. Worker crashes continue to recover via lease expiration. Stored and
client-visible execution errors use a generic message, without handler exception
messages or provider details.

`mcp_tasks_retry_waiting` counts queued Tasks whose persisted retry time is still
in the future. `mcp_tasks_failed` counts terminal failed Tasks retained within
TTL; it is a current gauge, not a cumulative failure counter or rate. Expired
Tasks are excluded. Delayed retries are excluded from
`mcp_tasks_capacity_waiting`, but remain in `mcp_tasks_outstanding`. Interpret
these together when diagnosing backlog and autoscaling.


### Task operational diagnostics

`gregale mcp tasks status --app <worker-app>` shows running Tasks, capacity waits,
delayed retries, retained failures, active worker registrations, unsupported
handler versions, and observer heartbeat freshness. JSON output adds stable
`diagnostics` entries with `code` and `message`, plus `worker_heartbeats_fresh`
and `observer_heartbeat_fresh`. Missing or stale metrics produce an unknown-health
explanation instead of being interpreted as zero work. Queue explanations include
`execution_capacity`, `retry_delay`, `failed_tasks_retained`,
`unsupported_handler`, `no_active_workers`, and `idle_queue`.

Workers register their supported tool/handler-version pairs in
`gregale_mcp_task_workers`. Heartbeats refresh at a bounded cadence (20–30 seconds,
depending on polling), expire after 90 seconds using database time, and are
withdrawn when a worker stops accepting work. Worker registrations describe
availability to claim work; they are not a guarantee that a handler succeeds.
Expired registrations do not participate in inventory and are pruned by workers.
The observer reads this table without writing registrations or migrating schema.
Initialize the updated schema through an upgraded web or worker process before
starting an upgraded observer. Include read access to the worker registry in any
observer database grants, and upgrade every worker so the inventory is complete.

`mcp_tasks_active_workers` counts unexpired registrations.
`mcp_tasks_unsupported_handler_tasks` counts unexpired queued Tasks and
expired-lease running Tasks with no matching version among registered workers.
Future retries and live executions are excluded; retry-attempt eligibility is
separate from handler compatibility. When there are no registered
workers the unsupported count is zero and compatibility is unknown; status reports
worker absence rather than claiming that all handlers are compatible.

Only the observer role publishes `mcp_tasks_observer_heartbeat` (value 1), after
its database read and aggregate metric publication succeed. Worker publishers do
not overwrite it. The CLI uses server-reported metric freshness; a fresh observer
heartbeat indicates a recent successful reporting cycle, not proof of continuous
uptime. Worker freshness means that the latest fresh aggregate reported a live
registration; allow for registry TTL and metric publication delays. Scale-to-zero
status reports an unknown observer when that heartbeat is absent or stale.
Telemetry contains aggregate counts and declared handler inventory, without Task
arguments, results, bearer tokens, caller identities, or exception messages.


### Task payload encryption key rotation

Keep `MCP_TASK_OWNER_KEY` stable for the namespace. It identifies callers and
supports legacy payloads; rotating payload encryption does not change ownership.
The store persists an irreversible ownership fingerprint and immutable key-ID
fingerprints, never the secrets themselves. Use a new namespace when intentionally
changing caller identity. Existing legacy payloads are authenticated before first
binding the ownership fingerprint.

Set `tasks.encryption_keys_env` to a distinct secret environment variable, such
as `MCP_TASK_PAYLOAD_KEYS`. Its value is JSON:

```json
{"activeKeyId":"2026_10","keys":{"2026_09":"<old secret>","2026_10":"<new secret>"}}
```

Each secret must contain at least 32 UTF-8 bytes; generate independent random
secrets rather than using the example placeholders. IDs contain 1–64 ASCII
letters, digits, underscores or hyphens. The ring supports at most 16 explicit
keys. `legacy` is reserved for the original owner-derived payload key and is
always available. Omit `encryption_keys_env` to retain legacy writes, or use
`activeKeyId: "legacy"` with a populated ring while preparing a rollout.

New encrypted fields carry an authenticated key ID. Reads select the matching
key; completing or resuming an older Task can use the new active key without
rewriting its arguments. Startup checks key usage across arguments, input state,
results, and errors for every unexpired Task, including terminal Tasks, and
rejects missing keys. Reusing a key ID with a different secret is rejected even
when old Tasks have expired. Payloads remain bound to namespace, Task ID and field.

Rotate in phases: upgrade all web and worker processes with the complete union
of old and new keys first; then activate the new key. Retain old keys until every
Task field using them has expired. Stop or reconfigure every writer still using
the old active key before removing it from configuration. Startup validation is
a snapshot and cannot prevent another already-running process with an obsolete
configuration from writing afterward; all participating processes must follow
the rollout order. This feature does not re-encrypt historical payloads or rotate
the ownership secret. Observers remain read-only and require no encryption secrets.

### Read-only Task preflight

After initializing the Task runtime, run `npm run doctor:tasks` with the worker's
bindings and `MCP_TASK_NAMESPACE`. The JSON report checks schema, runtime DML
permissions, retained encryption keys, live workers and eligible handler coverage.
Exit code 1 means failed or unknown readiness. No schema changes or Task claims
are made. Detailed database errors, keys and payloads are omitted. Repeat on every
writer during key rotation; this report cannot establish fleet-wide consistency.
For remote metrics and scale-to-zero checks, use `gregale mcp doctor --hosting
--app <worker-app> --preflight-path <this-directory>` with the same environment.

Task workers drain on SIGTERM/SIGINT. Set `tasks.shutdown_timeout_ms` (default
30000, range 1000–300000) below the platform termination grace period. New claims
stop immediately; active handlers renew leases until completion or the deadline.
At the deadline handlers receive an abort signal and leases expire naturally for
recovery. Dedicated workers exit after cleanup even if a handler ignores abort.
External effects must remain idempotent by Task ID. Available and draining
workers publish separate aggregate gauges; initialize the updated worker schema
before upgrading read-only observers.

Before retiring a handler, run `npm run check:task-compatibility` with the candidate
code and queue database/namespace bindings. It checks all unexpired nonterminal
Tasks against `mcpTaskHandlers`, including `previousVersions`, and returns counts
and latest expiry for missing versions. Delayed retries, input pauses and leased
work are included. Missing or unknown coverage returns exit code 1. The Task
doctor includes the same gate. Stop old-version producers before the final check:
this read-only snapshot does not fence future Task admission.

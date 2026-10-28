# MCP Python starter

A small stateless Streamable HTTP MCP server built on Python's standard
library, with PyJWT for external OAuth and psycopg for optional durable Tasks.
It advertises the `greet`, `add`, `stream_demo`, and `build_report` tools, a
welcome resource, a customer-record resource template and a summarize prompt.
It supports the current Gregale MCP protocol plus legacy discovery. It
is open-access by default and supports external OAuth with signed RS256/ES256
JWT validation and per-tool `tool_scopes`, `resource_scopes` and `prompt_scopes`.
It supports bounded prompt and resource completions, streamed progress, and
optional PostgreSQL-backed MCP Tasks for the sample `build_report` tool and
app-registered handlers.

```sh
python -m pip install -r requirements.txt
python -m unittest discover -s . -p 'test_*.py'
python server.py
gregale mcp server doctor --url http://127.0.0.1:8080/mcp --legacy
gregale mcp server doctor --url http://127.0.0.1:8080/mcp --stream-tool stream_demo
gregale mcp client call --url http://127.0.0.1:8080/mcp --tool add --arguments '{"a":7,"b":5}'
gregale mcp client complete --url http://127.0.0.1:8080/mcp --prompt summarize --argument style --value exec
gregale mcp server deploy --path . --name my-mcp
```

`gregale mcp server init --runtime python --path ./my-mcp` creates the starter files.
Read `gregale-mcp.json`, `server.py`, `auth.py`, and `tasks.py` before deploying.
Configure `auth.mode: "external-oauth"`, the issuer, JWKS URL, resource URL, endpoint
scopes, and each catalog entry's scopes before exposing private tools, resources
or prompts. Configured policy maps are allowlists: entries missing from a map
are hidden and denied. Resource scope keys can be exact URIs or simple URI
templates such as `customer://records/{recordId}`; each template variable
matches one URI segment. The sample rejects browser `Origin` headers unless explicitly allowlisted; it accepts
non-browser MCP clients that omit that header.

Durable Tasks are disabled by default. Set `tasks.enabled` in
`gregale-mcp.json`, bind the configured PostgreSQL URL, and provide a stable
`MCP_TASK_OWNER_KEY` with at least 32 bytes. Run the explicit migration command below before starting Tasks. Runtime accounts
need data access, not schema CREATE or table ownership. The starter advertises
`io.modelcontextprotocol/tasks` only when enabled and returns task handles for
registered handlers when clients opt in. `tasks/get`, `tasks/cancel`, and
`tasks/update`, and the caller-owned `task://tasks/{taskId}` resource are
supported. Set `request_approval: true` to pause a task for elicitation form
input; `tasks/get` exposes its outstanding `inputRequests`, and
`tasks/update` stores the encrypted response and resumes the worker. Calls from
other clients run synchronously unless they request approval, which requires
durable Tasks and client elicitation support.

Task key rotation is staged to remain safe during rolling deploys. First deploy
the rotation-capable starter everywhere, distribute a new key in
`previous_owner_keys` while the current key remains active, then switch the
active `owner_key_id` and keep the old key as previous. After that rollout
completes, set `payload_encryption_version` to `2`; live payloads are rewrapped
in bounded background batches. Retain the old owner key for at least 30 days
after the active-key rollout completes because caller ownership hashes cannot
be recomputed.

The Go and Python workers can share the Node task table, encryption key, and
namespace. New tasks use `build_report` handler version `2`; updated workers
also drain existing version `1` tasks. Set `MCP_TASKS_ROLE=web`
on the HTTP app and `MCP_TASKS_ROLE=worker` on a second app using the same
command to split serving from task execution. Both apps need the same explicit
`MCP_TASK_NAMESPACE`, PostgreSQL URL, and active/previous owner keys. The worker has no HTTP
listener. Set `MCP_TASKS_SCALING_APP_SLUG` and a `metrics:write`-scoped
`MCP_TASKS_SCALING_TOKEN` on the worker to publish aggregate queue metrics.
The worker publishes `mcp_tasks_without_compatible_worker` for claimable tasks
whose exact tool and handler version has no worker heartbeat within 45 seconds.
An optional `MCP_TASKS_ROLE=observer` process publishes metrics from a
read-only PostgreSQL credential with `SELECT` on the task and worker-handler
tables and does not need the owner key; use it when
the worker pool must scale to zero. Every non-combined role requires the same
explicit namespace. The observer also publishes bounded tool/version queue
counts; `gregale mcp server tasks status --details` reads its latest report from the
API without requiring local task database credentials. The Go and Python starters support `subscriptions/listen`
for task IDs and registered resource URIs, plus tool, resource, and prompt
list-change notifications. Call `notify_resource_updated(uri)` after changing
an application-owned resource. The default update adapter is process-local;
install a shared adapter with `set_resource_update_adapter` before starting the
HTTP server when resources can change on another web replica. Task updates use
PostgreSQL `LISTEN`/`NOTIFY`; each web process polls snapshots every five seconds
by default (or the configured task poll interval if longer) to recover missed
task events. Reconnecting starts with fresh snapshots;
`tasks/get` remains available as a fallback. The starter caps active task
subscriptions and watched IDs per process. Clients can also use
`gregale mcp client wait` and submit input replies through `tasks/update`.

The server bounds JSON request bodies, returns JSON responses without creating
sessions, and exposes `/healthz` for platform readiness. PyJWT validates
external OAuth tokens; install it with `requirements.txt`. Extend the catalogs
deliberately, keep schemas aligned with handlers, and add ownership checks to
resource reads that access application data.

## Runtime catalog changes

`server.py` exposes process-local `register_tool`, `replace_tool`,
`remove_tool`, `register_resource`, `replace_resource`, `remove_resource`,
`register_prompt`, `replace_prompt`, and `remove_prompt` functions. Add entries
before the `ThreadingHTTPServer` starts, or call the mutation functions from
application code while the process runs. For example:

```python
def lookup(arguments, caller):
    key = arguments.get("key")
    value = lookup_record(key, caller)
    return {"content": [{"type": "text", "text": value}]}


register_tool({
    "name": "lookup",
    "description": "Find a record by key.",
    "inputSchema": {
        "type": "object",
        "properties": {"key": {"type": "string"}},
        "required": ["key"],
    },
}, lookup)
```

Tool handlers validate arguments against the advertised schema and return a
normal MCP tool result. Resource handlers receive `(uri, variables, caller)`
and return a list of MCP resource contents. Prompt handlers receive
`(arguments, caller)` and return the MCP prompt result. `caller` contains the
verified subject, client ID, and scopes, but no bearer token. Mutations send
list-change notifications only when the subscribing caller's authorized view
changed. In external OAuth mode, add dynamic entry names and resource patterns
to the corresponding scope allow-lists. The registry is process-local, so
every serving replica needs the same application registration code.

Register durable handlers in `server.py` before its `main()` call starts the
runtime. The same registrations must be present in web and worker processes
sharing the queue. Calls from clients that advertise MCP Tasks are queued for
registered handlers; other clients continue through the regular tool handler.
The validator runs before task arguments are encrypted and stored.

```python
def validate_lookup(arguments):
    if set(arguments) != {"key"} or not isinstance(arguments["key"], str) or not arguments["key"].strip():
        raise ValueError("key is required")


def lookup_task(arguments, task):
    value = lookup_durably(arguments["key"], idempotency_key=task.task_id)
    return {"content": [{"type": "text", "text": value}]}


register_task_handler(
    "lookup",
    version="1",
    validate=validate_lookup,
    execute=lookup_task,
)
```

Also register the matching MCP tool with `register_tool`; its schema must agree
with the task validator. `TaskExecutionContext` provides `task_id`, a
cooperative `cancelled` event, and `request_input`/`request_inputs` methods for
client input. If input is outstanding, those methods raise `TaskInputRequired`;
let it propagate so the task pauses and restarts after the client responds.
Keep older executors in `previous_versions` until their queued tasks drain.
Use the same handler versions in every process sharing the queue; workers only
claim versions they registered.

`gregale mcp server deploy` reads this digest from its zero-traffic candidate and pins
the promoted deployment revision with `MCP_CATALOG_EXPECTED_SHA256`. For other
deploy paths, set that variable to the `/healthz` digest on every serving
replica. Remove any existing app-wide env or secret with this key before using
`gregale mcp server deploy`; those runtime layers take precedence over deployment
overrides. A mismatch makes `/healthz` return 503 and blocks MCP requests.
The health response includes Gregale's `FAAS_INSTANCE_ID`. In addition,
`gregale mcp server status` receives the selected replica identity from Gregale's
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

# mcp-python

A small Python MCP server using the official MCP Python SDK. It serves stateless
Streamable HTTP at `/mcp`, exposes three tools, a static resource, a resource
template and a prompt, and reports readiness at `/healthz`. It starts in open
mode and rejects browser `Origin` headers unless you add the exact origin to
`allowed_origins`.

It uses the official MCP Python SDK 2.3.0 and Uvicorn 0.54.0.

The starter supports application-side external OAuth resource-server checks,
including RS256/ES256 JWT verification against a remote JWKS, protected-resource
metadata, bearer challenges, and per-entry scope checks for tools, resources,
resource templates and prompts. The OAuth provider owns login and token
issuance. It supports both current and legacy protocol versions over Streamable
HTTP, without the deprecated HTTP+SSE transport. It advertises safe,
prefix-matched completions for `summarize.style` and the
`customer://records/{recordId}` resource template; completion access follows
the same prompt and resource scope policies as prompt reads and resource reads,
and customer-record suggestions are filtered by verified caller ownership.

For OAuth, set `auth.mode` to `external-oauth`, provide HTTPS `issuer`,
`jwks_url` and canonical `resource` URLs, and set nonempty endpoint `scopes` in
`gregale-mcp.json`. `tool_scopes`, `resource_scopes` and `prompt_scopes` are
optional allowlists; an empty scope array allows any caller with the endpoint
scopes, and an omitted map keeps endpoint-only access for that catalog type.
The default open config lists the harmless sample entries with empty arrays.
Resource and tool handlers can use the injected `Context` to read
`caller_identity_for_request(context)`, which returns the verified token subject
and client ID without exposing the token. The sample customer records are owned
by `demo-caller-a` (`example-1`, `example-3`) or `demo-caller-b` (`example-2`);
open mode keeps these synthetic examples public. Replace the sample owner map
with a database lookup scoped by the verified subject before serving real
customer data.

## Durable MCP Tasks

`build_report` supports the Gregale Tasks extension when `tasks.enabled` is true.
Set `DATABASE_URL` to a PostgreSQL connection string, provide a stable
`MCP_TASK_OWNER_KEY` secret with at least 32 bytes, and set `FAAS_APP_ID` to a
stable namespace. The default config names these environment variables already;
enable Tasks by setting `tasks.enabled` to `true` in `gregale-mcp.json`.

When the table is absent, startup creates the base `gregale_mcp_tasks` schema; for
an existing migrated schema it checks the required columns without trying to
change database objects. A fresh database therefore needs schema-create rights,
while a migrated runtime role needs table DML rights. Arguments, results and errors
are encrypted with AES-256-GCM; task ownership is scoped to the
verified OAuth resource, subject and client ID. Tasks from open-mode requests
share an `open` owner. The default queue limits are 1,000 outstanding tasks per
namespace and 100 per owner. One in-process worker claims tasks with a lease, so a
restart recovers queued tasks and reclaims expired running leases. Keep the owner
key stable while task rows remain in PostgreSQL.

For a shared task database with the Node starter, use the same namespace, owner
key and OAuth resource. For independent payload-key rotation, set
`tasks.encryption_keys_env` to a distinct sealed-secret environment variable
containing the same `activeKeyId`/`keys` JSON key ring on web and worker replicas.
Go and Python support Node-compatible v2 payload envelopes as well as legacy
reads. Distribute old and new keys before activating the new key, and retain
keys while Tasks encrypted with them remain. The observer does not need payload
or owner keys.

Use a Task-capable client to call the sample tool, then read or cancel its task:

```sh
gregale mcp client call --url http://127.0.0.1:8080/mcp --tasks \
  --tool build_report --arguments '{"report":"weekly","steps":8}'
gregale mcp client wait --url http://127.0.0.1:8080/mcp --task-id TASK_ID
gregale mcp client cancel --url http://127.0.0.1:8080/mcp --task-id TASK_ID
```

The starter's `tasks/update` endpoint follows the protocol response contract; its
sample handler does not request client input. If the PostgreSQL database is also
used by the Node worker, run the Node `tasks:migrate` workflow to prepare its
worker, admission and key-registry tables.

## Run locally

```sh
python -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
python -m unittest
python server.py
gregale mcp server doctor --url http://127.0.0.1:8080/mcp
gregale mcp client tools --url http://127.0.0.1:8080/mcp
gregale mcp client call --url http://127.0.0.1:8080/mcp --tool greet --arguments '{"name":"Ada"}'
gregale mcp client resources --url http://127.0.0.1:8080/mcp
gregale mcp client read --url http://127.0.0.1:8080/mcp --uri 'customer://records/example-1'
gregale mcp client prompts --url http://127.0.0.1:8080/mcp
gregale mcp client render --url http://127.0.0.1:8080/mcp --prompt summarize --arguments '{"text":"weekly report"}'
gregale mcp client complete --url http://127.0.0.1:8080/mcp --prompt summarize --argument style --value exec
gregale mcp client complete --url http://127.0.0.1:8080/mcp --resource-template 'customer://records/{recordId}' --argument recordId --value example-
```

For a protected deployment, pass an MCP access token with `--token-env
MCP_TOKEN`. Keep that token out of shell history. Add object or tenant ownership
checks inside resource and tool handlers before connecting real customer data.

## Deploy

```sh
gregale mcp server deploy --path . --name <slug>
```

The deploy command stages a zero-traffic candidate, verifies its MCP endpoint,
and promotes only after the previous serving revision still owns all traffic.

## Browser clients

Configure exact origins in `allowed_origins`. The MCP endpoint accepts browser
OPTIONS preflight before authentication and allows GET/POST with Authorization,
Content-Type, Accept, MCP-Protocol-Version, MCP-Method, MCP-Name and valid
MCP-Param-* headers. Responses expose WWW-Authenticate, MCP-Protocol-Version and
Streaming-Status, including authorization failures and streamed responses.
Untrusted origins, unsupported requested methods/headers and duplicate Origin
headers are rejected. Actual MCP requests still require the configured bearer
and scopes; cookies and credentialed CORS are not enabled.

## Task schema setup and upgrade

Run schema setup from a trusted operator environment using a schema-owner account:

```sh
python task_schema.py apply
python task_schema.py status
```

Both commands require `MCP_TASK_MIGRATION_DATABASE_URL`; they never fall back to
DATABASE_URL and do not start HTTP serving, workers or observers. Bind the
migration credential only to this operator process. Server processes use their
configured `tasks.database_url_env` binding and only validate installed schema
version, required columns and the enabled notification trigger. Startup fails
with migration guidance if that schema is missing or incompatible.

Use a dedicated schema without CREATE privileges for PUBLIC. Configure the same
search_path (that schema, then pg_catalog) for migration and service accounts.
For example, after migration, grant the existing runtime role USAGE on that
schema, SELECT/INSERT/UPDATE/DELETE on gregale_mcp_tasks and
gregale_mcp_task_worker_handlers, and SELECT on gregale_mcp_task_starter_schema.
An observer needs SELECT on all three tables and USAGE on the schema; its existing
read-only privilege checks still apply. Do not grant schema CREATE, table
ownership, TRIGGER or write access to the version table to service accounts.

For existing queues, run apply once before deploying these updated starters.
It retains Task rows and installs the version marker transactionally. Repeat
apply is supported; a future schema version is rejected rather than downgraded.
Go and Python share this marker. For a queue also used by Node, retain Node's
own migration and admission setup; this command does not replace it.

## Protocol telemetry

The server emits structured `mcp_protocol_request` events from actual parsed RPC
methods and response objects, including tool errors returned with HTTP 200.
Method and tool categories are fixed; custom tool names use `other`. Events do
not contain arguments, results, resource URIs, error messages, bearer values or
RPC IDs. Valid UUID `X-Faas-Request-Id` and nonzero version-00 `traceparent` IDs
support correlation with Gregale request evidence; they are not trusted identity.

`execution_duration_ms` ends at response construction (subscription acknowledgment
for subscription setup). `stream_duration_ms` measures SSE lifetime separately;
the durations overlap. Unobserved timings are omitted. Protocol outcomes and
stream termination are separate, so delivery failure does not erase an observed
result. Task creation measures admission, not background completion. Typed
termination distinguishes cancellation and timeout without adding tool deadlines.

Aggregate only this event family into counters/histograms. This is structured
logging, not a public metrics endpoint. Pre-parse/pre-dispatch authentication
failures and notifications are outside coverage. Keep correlation IDs out of
metric labels and use existing log retention/access controls.

### Telemetry heartbeats

The web process emits a payload-free `mcp_telemetry_heartbeat` immediately and
every 30 seconds, independent of RPC traffic. Shutdown stops the emitter.
This is process telemetry, not an MCP readiness check. Set
`MCP_TELEMETRY_LOSS_ALERT=true` to opt into platform operator alerts for lost
heartbeats; customer performance webhook settings are separate.

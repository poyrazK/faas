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
gregale mcp doctor --url http://127.0.0.1:8080/mcp --legacy
gregale mcp doctor --url http://127.0.0.1:8080/mcp --stream-tool stream_demo
gregale mcp call --url http://127.0.0.1:8080/mcp --tool add --arguments '{"a":7,"b":5}'
gregale mcp complete --url http://127.0.0.1:8080/mcp --prompt summarize --argument style --value exec
gregale mcp deploy --path . --name my-mcp
```

`gregale mcp init --runtime python --path ./my-mcp` creates the starter files.
Read `gregale-mcp.json`, `server.py`, `auth.py`, and `tasks.py` before deploying.
Configure `auth.mode: "external-oauth"`, the issuer, JWKS URL, resource URL, endpoint
scopes, and each catalog entry's scopes before exposing private tools, resources
or prompts. Configured policy maps are allowlists: entries missing from a map
are hidden and denied. Resource scope keys can be exact URIs or simple URI
templates such as `customer://records/{recordId}`; each template variable
matches one URI segment. The sample rejects every browser `Origin`; it accepts
non-browser MCP clients that omit that header.

Durable Tasks are disabled by default. Set `tasks.enabled` in
`gregale-mcp.json`, bind the configured PostgreSQL URL, and provide a stable
`MCP_TASK_OWNER_KEY` with at least 32 bytes. The database role needs permission
to create and alter the task table and indexes. The starter advertises
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
counts; `gregale mcp tasks status --details` reads its latest report from the
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
`gregale mcp task-wait` and submit input replies through `tasks/update`.

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

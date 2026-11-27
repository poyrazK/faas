# MCP Python starter

A small stateless Streamable HTTP MCP server built on Python's standard
library, with PyJWT for external OAuth.
It advertises the `greet`, `add`, `stream_demo`, and `build_report` tools, a
welcome resource, a customer-record resource template and a summarize prompt.
It supports the current Gregale MCP protocol plus legacy discovery. It
is open-access by default and supports external OAuth with signed RS256/ES256
JWT validation and per-tool `tool_scopes`, `resource_scopes` and `prompt_scopes`.
It supports bounded prompt and resource completions and streamed progress.
Every tool call is answered in its response; this starter has no durable MCP
Tasks.

The HTTP endpoint accepts Content-Length or chunked request framing. Decoded
bodies and aggregate chunk metadata are each limited to 1 MiB, with chunk-size
and trailer lines limited to 64 KiB. Trailers cannot change authentication or MCP
headers. Malformed or ambiguous framing receives an error and closes the
connection; valid requests can reuse the connection.

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
Read `gregale-mcp.json`, `server.py` and `auth.py` before deploying.
Configure `auth.mode: "external-oauth"`, the issuer, JWKS URL, resource URL, endpoint
scopes, and each catalog entry's scopes before exposing private tools, resources
or prompts. Configured policy maps are allowlists: entries missing from a map
are hidden and denied. Resource scope keys can be exact URIs or simple URI
templates such as `customer://records/{recordId}`; each template variable
matches one URI segment. The sample rejects browser `Origin` headers unless explicitly allowlisted; it accepts
non-browser MCP clients that omit that header.

The starter supports `subscriptions/listen` for registered resource URIs, plus
tool, resource, and prompt list-change notifications. Call `notify_resource_updated(uri)` after changing
an application-owned resource. The default update adapter is process-local;
install a shared adapter with `set_resource_update_adapter` before starting the
HTTP server when resources can change on another web replica.

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

The starter validates a custom tool's declared `inputSchema` before invoking
its handler. JSON Schema validation supports local references,
does not retrieve external references, and does not coerce values or insert
defaults. Boolean property schemas, including nested `true` and `false` schemas,
work with modern request headers; argument validation still enforces them.
Handlers enforce business rules and ownership and return a normal
MCP tool result. Successful custom tool handler results must include
`structuredContent` matching a declared `outputSchema`; `isError: true`
results and intermediate `input_required` responses may omit it. Both schemas
are checked when registering or replacing the tool, and invalid results
produce a redacted tool error.
Resource handlers receive `(uri, variables, caller)`
and return a list of MCP resource contents. Prompt handlers receive
`(arguments, caller)` and return the MCP prompt result. `caller` contains the
verified subject, client ID, and scopes, but no bearer token. Mutations send
list-change notifications only when the subscribing caller's authorized view
changed. In external OAuth mode, add dynamic entry names and resource patterns
to the corresponding scope allow-lists. The registry is process-local, so
every serving replica needs the same application registration code.

For custom resource subscriptions, pass `authorize=` to `register_resource` and
`replace_resource`. The synchronous callback receives `(uri, context)` with the
verified subject, client ID and scopes; return `True` to permit access. Reads,
subscription setup and update delivery share the callback. Custom resources
without a callback remain readable through their handlers but cannot be subscribed.

`gregale mcp server deploy` reads this digest from its zero-traffic candidate and pins
the promoted deployment revision with `MCP_CATALOG_EXPECTED_SHA256`. For other
deploy paths, set that variable to the `/healthz` digest on every serving
replica. Remove any existing app-wide env or secret with this key before using
`gregale mcp server deploy`; those runtime layers take precedence over deployment
overrides. A mismatch makes `/healthz` return 503 and blocks MCP requests.
The health response includes the serving instance from Gregale's
`X-Faas-Instance-Id` request header. (`FAAS_INSTANCE_ID` is set at boot, so every
instance restored from one snapshot would report the same value.) In addition,
`gregale mcp server status` receives the selected replica identity from Gregale's
authenticated exact-instance probes. It initializes MCP and lists the
caller-visible catalogs on every running replica without invoking tools,
reading resources, or relying on load-balancer sampling. Use `--token-env` or
`--release-policy` for external OAuth endpoints. The
digest covers advertised definitions, OAuth per-entry allow-lists, not handler
implementation; deploy the same reviewed build to all
replicas.

## Durable MCP Tasks

This starter answers every tool call in its response and ships without durable
Task code. For long-running tools that return a durable MCP Task handle
(PostgreSQL task store, workers, encrypted payloads), start from the Tasks
starter instead:

```
gregale init --template mcp-python-tasks --path ./my-mcp
gregale mcp server init --runtime python --tasks --name <prefix> --path ./my-mcp
```

## Graceful shutdown

On SIGTERM/SIGINT the server stops accepting new requests and waits for open
HTTP connections, including progress streams, to finish. Set
`MCP_DRAIN_TIMEOUT_SECONDS` (default 10; range 1–300) below the worker's
`drain_timeout`, with at least 10 seconds left for cleanup. Observe the request
cancellation signal in long-running tools.

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
result. Typed
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

## Deploy a typed application toolkit

Use the Gregale SDK preview to compose query, resource, pipeline, and action
adapters into a toolkit. Call `main(toolkit=toolkit)` from your application entry point.
Toolkits require external OAuth. Select each operation and assign its scopes
when mounting its adapter. Keep existing tool or resource policies explicitly
listed in `gregale-mcp.json` when you want them alongside the toolkit. The
application adapters enforce current business access and any human approvals.

To expose selected pipeline starts as MCP Tasks, configure the SDK pipeline
Task view and pass `pipeline_tasks` with the toolkit. Apply the pipeline Task binding
schema first and share its database, namespace, and owner key across serving
replicas. Use `tasks/get` to follow current progress and read the typed result.

# MCP on Gregale

This starter has three harmless tools, a public welcome resource, a customer
record URI template, and a summarize prompt with argument completions. It uses stateless Streamable HTTP,
request-scoped SSE progress, cancellation and optional external OAuth JWT
authentication. It supports MCP 2026-07-28 and stateless compatibility with
2025-11-25.

```
npm ci
npm test
npm start
gregale mcp server doctor --url http://127.0.0.1:8080/mcp --legacy
gregale mcp client call --url http://127.0.0.1:8080/mcp --tool add --arguments '{"a":7,"b":5}'
gregale mcp client complete --url http://127.0.0.1:8080/mcp --prompt summarize --argument style --value exec
gregale mcp server deploy --path . --name my-mcp --profile small
gregale mcp server export --url https://my-mcp.gregale.app/mcp --name my-mcp
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
  "resource": "https://my-mcp.gregale.app/mcp",
  "scopes": ["mcp:tools"],
  "tool_scopes": {
    "greet": [],
    "add": ["math:read"],
    "stream_demo": ["mcp:stream"]
  },
  "resource_scopes": {
    "greeting://welcome": [],
    "customer://records/{recordId}": ["records:read"]
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
tenant/object inside the callback using `verifiedCallerIdentity(ctx.http.authInfo)`;
it returns the verified `subject` and `clientId` without exposing the token. Never
use a tenant ID from arguments as the caller identity. The sample records are
owned by `demo-caller-a` (`example-1`, `example-3`) and `demo-caller-b`
(`example-2`); open mode keeps these synthetic examples public.
This is application policy, so custom servers must implement their own
enforcement.

## Prompt and resource completions

The `summarize` prompt completes its optional `style` argument with `brief`,
`technical` and `executive`. The customer-record template completes only the
three harmless sample IDs included in the starter, filtered by the verified
caller's sample-record ownership in external OAuth mode. Try either through the CLI:

```sh
gregale mcp client complete --url http://127.0.0.1:8080/mcp \
  --prompt summarize --argument style --value exec
gregale mcp client complete --url http://127.0.0.1:8080/mcp \
  --resource-template 'customer://records/{recordId}' --argument recordId --value example-
```

Use `--context` or `--context-file` to pass other already-resolved string
arguments for dependent suggestions. MCP completions are not tool calls. Keep
suggestions bounded and caller-scoped; never return customer IDs or other private
values unless the current caller is authorized to see them.
Completion requests pass through the same per-entry prompt and resource scope
checks as `prompts/get` and `resources/read` before suggestions are computed.

Use a client token via `--token-env MCP_TOKEN`; Gregale CLI account credentials are
never forwarded to this server. Capture `mcp lock` baselines with the same identity
and scopes when comparing a candidate server.

Keep durable state outside the VM. Avoid fetching customer credentials before
checkpointing; fetch short-lived credentials during a verified tool request.
Tool logs contain only name, duration and outcome. Calls are not automatically
retried. Doctor lists tools without executing them. An explicit `--stream-tool
stream_demo` doctor probe runs that tool and verifies spaced progress events.

## Durable MCP Tasks

This starter answers every tool call in its response. For long-running tools
that return a durable MCP Task handle (PostgreSQL task store, workers, gated
Task releases), start from the Tasks starter instead:

```
gregale init mcp-node-tasks
gregale mcp server init --runtime node --tasks
```

## Runtime catalog changes

`createApp` returns process-local catalog operations. Register entries before
starting the HTTP listener, or call them from application code while it runs:

```js
import * as z from 'zod/v4';

const mcp = createApp(config);
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
Visible-ASCII names such as `constructor` and `__proto__` are supported for tools,
prompts and resource templates. Replacing the sample `customer_record` resource
uses the supplied URI/template, data handler and completion callbacks; the sample
record guard belongs only to the original built-in handler.
Handlers must validate their own tool arguments and enforce ownership for
application data. Successful catalog changes notify active subscribers when
that caller's authorized catalog changed. In external OAuth mode, add every
dynamic name or resource URI pattern to the matching scope allow-list; missing
policy entries stay hidden and denied. The registry is local to one process, so
every serving replica needs the same application registration code.

Resource subscriptions require a synchronous `authorize(uri, {authInfo, config})`
callback as the fifth argument to `registerResource`/`replaceResource`. Return
`true` only for a verified caller allowed to read the resource. The same callback
guards reads, subscription setup and every update. Omitting it preserves existing
read-handler checks but denies subscriptions to custom resources.

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
digest covers advertised definitions and OAuth per-entry allow-lists, not
handler implementation; deploy the same reviewed build to all
replicas.

## Graceful shutdown

On SIGTERM/SIGINT the server stops accepting new requests and waits for open
HTTP connections, including progress streams, to finish. Set
`MCP_DRAIN_TIMEOUT_SECONDS` (default 10; range 1–300) below the worker's
`drain_timeout`, with at least 10 seconds left for cleanup. At the deadline the
remaining connections are closed. Observe the request cancellation signal in
long-running tools.

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
adapters into a toolkit. Pass it as `toolkit` in the options to `createApp` in `server.js`.
Toolkits require external OAuth. Select each operation and assign its scopes
when mounting its adapter. Keep existing tool or resource policies explicitly
listed in `gregale-mcp.json` when you want them alongside the toolkit. The
application adapters enforce current business access and any human approvals.

To expose selected pipeline starts as MCP Tasks, configure the SDK pipeline
Task view and pass `pipelineTasks` with the toolkit. Apply the pipeline Task binding
schema first and share its database, namespace, and owner key across serving
replicas. Use `tasks/get` to follow current progress and read the typed result.

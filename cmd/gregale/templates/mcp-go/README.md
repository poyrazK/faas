# MCP Go starter

A small stateless Streamable HTTP MCP server using Go's standard HTTP library. It advertises the `greet`, `add`,
`stream_demo`, and `build_report` tools, a welcome resource, a customer-record
resource template and a summarize prompt.
It supports the current
Gregale MCP protocol plus legacy discovery. It is open-access by default and
supports external OAuth with signed RS256/ES256 JWT validation and per-tool
`tool_scopes`, `resource_scopes` and `prompt_scopes`. It supports bounded prompt
and resource completions and streamed progress. Every tool call is answered in
its response; this starter has no durable MCP Tasks.

# mcp-go

A small Go MCP server using the official Go SDK. It serves stateless Streamable
HTTP at `/mcp`, exposes three tools, a static resource, a resource template and a
prompt, and reports readiness at `/healthz`. It starts in open mode and rejects
browser `Origin` headers unless you add the exact origin to `allowed_origins`.

The generated `go.mod` declares `go 1.26.11` and
`github.com/modelcontextprotocol/go-sdk v1.8.0`. It also lists `pgx`, which only
the Tasks starter uses; `go mod tidy` removes it.

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
Handlers can call `verifiedCallerIdentity(principal)` to get the verified token
subject and client ID without carrying the token into application code. The
sample customer records are owned by `demo-caller-a` (`example-1`, `example-3`)
or `demo-caller-b` (`example-2`); open mode keeps these synthetic examples
public. Replace the sample owner map with a database lookup scoped by the
verified subject before serving real customer data.

## Run locally

`gregale mcp server init --language go` materializes `go.mod`. Then run:

```sh
go test ./...
go run .
gregale mcp server doctor --url http://127.0.0.1:8080/mcp --legacy
gregale mcp server doctor --url http://127.0.0.1:8080/mcp --stream-tool stream_demo
gregale mcp client call --url http://127.0.0.1:8080/mcp --tool add --arguments '{"a":7,"b":5}'
gregale mcp client complete --url http://127.0.0.1:8080/mcp --prompt summarize --argument style --value exec
gregale mcp server deploy --path . --name my-mcp
```

`gregale mcp server init --runtime go --path ./my-mcp` writes the module file used for
local development. Read `gregale-mcp.json`, `main.go` and `auth.go`
before deploying. Configure `auth.mode: "external-oauth"`, the issuer, JWKS URL,
resource URL, endpoint scopes, and each catalog entry's scopes before exposing
private tools, resources or prompts. Configured policy maps are allowlists:
entries missing from a map are hidden and denied. Resource scope keys can be
exact URIs or simple URI templates such as
`customer://records/{recordId}`; each template variable matches one URI
segment. The sample rejects browser `Origin` headers unless explicitly allowlisted; it accepts non-browser MCP
clients that omit that header.

The starter supports `subscriptions/listen` for registered resource URIs, plus
tool, resource, and prompt list-change notifications. Call `NotifyResourceUpdated(uri)` after changing an
application-owned resource. The default update adapter is process-local; install
a shared `MCPResourceUpdateAdapter` with `SetMCPResourceUpdateAdapter` before
serving requests when resources can change on another web replica.

The HTTP handler supports JSON-RPC requests and notifications without creating
sessions. It accepts only bounded JSON request bodies, returns JSON responses,
and exposes `/healthz` for platform readiness. Extend its catalogs deliberately,
keep schemas aligned with handlers, and add ownership checks to resource reads
that access application data.

## Runtime catalog changes

The starter exposes process-local `RegisterMCPTool`, `ReplaceMCPTool`, and
`RemoveMCPTool`, plus `RegisterMCPResource`/`ReplaceMCPResource`/
`RemoveMCPResource` and `RegisterMCPPrompt`/`ReplaceMCPPrompt`/
`RemoveMCPPrompt`. Call registration before `ListenAndServe`, or call the
mutation functions from application code while the process runs. For example:

```go
if err := RegisterMCPTool(map[string]any{
    "name": "lookup",
    "description": "Find a record by key.",
    "inputSchema": map[string]any{
        "type": "object",
        "properties": map[string]any{"key": map[string]any{"type": "string"}},
        "required": []string{"key"},
    },
}, func(ctx context.Context, args map[string]any, caller *MCPRequestContext) (map[string]any, error) {
    key, _ := args["key"].(string)
    value, err := lookupRecord(ctx, key, caller)
    if err != nil {
        return nil, err
    }
    return map[string]any{"content": []map[string]string{{"type": "text", "text": value}}}, nil
}); err != nil {
    log.Fatal(err)
}
```

The starter validates a custom tool's declared `inputSchema` before invoking
its handler. Draft 7 and 2020-12 schemas and local references
are supported; external references are rejected at registration. Validation
does not coerce values or insert defaults. Handlers still enforce business
rules and ownership. Successful custom tool handler results must include
`structuredContent` matching a declared `outputSchema`; `isError: true`
results and intermediate `input_required` responses may omit it. Both schemas
are compiled when registering or replacing the tool, and invalid results
produce a redacted tool error. Resource
handlers return `[]map[string]any` contents and receive URI-template variables;
prompt handlers return the MCP prompt result. `MCPRequestContext` includes
verified subject, client ID, and scopes, but no bearer token. Mutations send
list-change notifications only when the subscribing caller's authorized view
changed. In external OAuth mode, add dynamic entry names and resource patterns
to the corresponding scope allow-lists. The registry is process-local, so
every serving replica needs the same application registration code.

Custom resource subscriptions require an `MCPResourceAuthorizer` passed to
`RegisterMCPResource` or `ReplaceMCPResource`. It receives context, URI and verified
caller context, and must return true only for authorized readers. Reads,
subscription setup and delivery share this side-effect-free check. Custom resources
without an authorizer retain their read handlers but deny subscriptions.

## Durable MCP Tasks

This starter answers every tool call in its response and ships without durable
Task code. For long-running tools that return a durable MCP Task handle
(PostgreSQL task store, workers, encrypted payloads), start from the Tasks
starter instead:

```
gregale init --template mcp-go-tasks --path ./my-mcp
gregale mcp server init --runtime go --tasks --name <prefix> --path ./my-mcp
```

## Graceful shutdown

On SIGTERM/SIGINT the server stops accepting new requests and waits for open
HTTP connections, including progress streams, to finish. Set
`MCP_DRAIN_TIMEOUT_SECONDS` (default 10; range 1–300) below the worker's
`drain_timeout`, with at least 10 seconds left for cleanup. Observe the request
cancellation signal in long-running tools.

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

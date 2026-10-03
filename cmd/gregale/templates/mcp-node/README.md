# MCP on Gregale

This starter has three harmless tools, stateless Streamable HTTP, request-scoped
SSE progress, cancellation and optional external OAuth JWT authentication. It
supports MCP 2026-07-28 and stateless compatibility with 2025-11-25.

```
npm ci
npm test
npm start
gregale mcp doctor --url http://127.0.0.1:8080/mcp --legacy
gregale mcp call --url http://127.0.0.1:8080/mcp --tool add --arguments '{"a":7,"b":5}'
gregale mcp deploy --path . --name my-mcp --profile small
gregale mcp config --url https://my-mcp.gregale.dev/mcp --name my-mcp
```

`gregale-mcp.json` declares the MCP contract. `gregale.yaml` declares ordinary
HTTP hosting. The deploy command uploads the worktree, requires a streaming-enabled
plan, opens the platform public auth gate and verifies tool discovery. An empty
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
    "stream_demo": ["mcp:stream"]
  }
}
```

Configure the provider for OAuth 2.1, PKCE, authorization-server discovery and the
canonical MCP resource audience. The resource server verifies RS256/ES256 JWT
access tokens with an expiry and subject, issuer, audience and all required
scopes. Opaque tokens need a provider-specific introspection adapter. The provider
owns registration, login and consent; this starter does not implement those flows.
`auth.scopes` cover the endpoint. `auth.tool_scopes` adds scopes required for each
tool: all listed scopes must be present in the verified JWT. `[]` permits every
endpoint caller; an omitted tool is denied when the map is configured. An empty
map denies all tools. Open mode accepts only empty scope arrays, and the generated
starter explicitly allows its three harmless tools. Null policies/arrays fail
validation. Omitting the entire map retains endpoint-only compatibility.

Add tools through the `registerTool` helper in `app.js` and add their policy to
`gregale-mcp.json`. Each request gets a fresh catalog filtered by verified scopes.
Calls to hidden tools fail before execution; insufficient scopes produce a 403
bearer challenge with the additional required scopes. The callback guard also
checks the SDK's verified request context. Client headers, arguments and tool
annotations cannot grant permission. Check ownership of any tenant/object inside
the tool using verified identity (`ctx.http.authInfo.extra.subject`), never an
unverified tenant ID from arguments. This is application policy, so custom servers
must implement their own enforcement.

Use a client token via `--token-env MCP_TOKEN`; Gregale CLI account credentials are
never forwarded to this server. Capture `mcp lock` baselines with the same identity
and scopes when comparing a candidate server.

Keep durable state outside the VM. Avoid fetching customer credentials before
checkpointing; fetch short-lived credentials during a verified tool request.
Tool logs contain only name, duration and outcome. Calls are not automatically
retried. Doctor lists tools without executing them. An explicit `--stream-tool
stream_demo` doctor probe runs that tool and verifies spaced progress events.

# MCP servers on Gregale

The MCP hosting profile is preview for tool servers that implement `tools/list`.
Resources-only or prompts-only servers can use ordinary app deployment but do not
pass this profile's qualification. It uses ordinary HTTP applications and
stateless Streamable HTTP, with optional stateless legacy compatibility. Builds
containing ADR-426 provide the commands below; check `gregale mcp --help`.
The profile requires a streaming-enabled Hobby, Pro, or Scale account and a
gateway with streaming enabled.

```sh
gregale mcp init --path ./my-mcp
cd my-mcp
npm ci
npm test
npm start
```

In another terminal:

```sh
gregale mcp doctor --url http://127.0.0.1:8080/mcp --legacy --stream-tool stream_demo
gregale mcp call --url http://127.0.0.1:8080/mcp --tool add --arguments '{"a":7,"b":5}'
gregale mcp deploy --path . --name my-mcp --profile small
gregale mcp tools --app my-mcp
gregale mcp config --app my-mcp --name my-mcp
```

The starter binds loopback locally and the guest interface on Gregale.
`gregale.yaml` controls start, port and health. `gregale-mcp.json` controls the
MCP endpoint, stateless mode, legacy compatibility, trusted browser origins and
client authentication. The default starter explicitly allows public access to
three harmless tools. An empty allowed-origins list rejects all browser origins;
add exact origins for trusted browser clients.

`mcp deploy` uploads the current worktree, waits for ordinary deployment readiness,
enables streaming, and opens the platform ingress auth gate for the application's
client-auth policy. It then verifies tool discovery, Origin rejection and declared
legacy compatibility. A failed check returns a failing receipt and enables
maintenance to stop public requests. Review and fix the failure before clearing
maintenance with `gregale app my-mcp --no-maintenance` and deploying again.
Post-deploy verification does not roll the application back or execute tools.

Doctor reports the real response's gateway streaming classification, wake tier
and client-observed request duration. Duration includes network and application
time. `--stream-tool` explicitly runs the named tool and checks spaced progress
events; use a harmless probe tool. Discovery alone cannot establish unbuffered
delivery. MCP JSON-RPC errors, truncated streams and `isError` tool results fail
the appropriate command even when HTTP returns 200. Calls are never retried.
Tools with invalid parameter-header annotations are excluded from discovery;
`tools` and `doctor` report their names and rejection reasons in `rejected_tools`.
Other valid tools remain available. A malformed response or duplicate tool name
still fails discovery.

## Tool contract snapshots

Capture a caller-visible tool interface before changing a server:

```sh
gregale mcp lock --app my-mcp --out baseline.json
gregale mcp lock --url https://candidate.example.com/mcp --out candidate.json
gregale mcp diff --before baseline.json --after candidate.json --check --json
```

`lock` discovers tools without executing them. Its default destination is
`gregale-mcp.lock.json`; `--legacy` captures the stateless 2025-11-25 interface.
Snapshots contain the protocol version, tool names, descriptions, input/output
schemas and annotations. They omit endpoint URLs, timestamps and client credentials.
Tool names and JSON object keys are sorted, and schema numbers keep their precision.
Discovery that rejects any tool cannot produce a complete snapshot and fails
without writing a file. Existing snapshots require a new `--out` or explicit
`--force`; writes are atomic with private file permissions and reject symlinks.

Use the same authorization context for both captures: a caller's scopes can change
which tools are visible. Review tool metadata before committing it; it is supplied
by the server and may contain private information. Snapshots do not record the
identity or permissions used to capture them.

`diff` reads two local files without making network requests. It reports removed
tools/properties, new required inputs, narrowed input types/enums, weakened output
guarantees, metadata changes and changes needing review. Required-field, type-set
and enum ordering is ignored. Protocol and annotation changes need review;
annotations never grant execution permission. Other changed schema keywords,
including constraints, references and combinators, need review. References are
preserved without fetching them. Changed subtrees beyond 64 property/item levels
also need review; snapshots retain their full contents. The comparison does not prove arbitrary
JSON Schema compatibility or unchanged tool behavior. Property removal is treated
conservatively as breaking even where JSON Schema would still permit that key.

Without `--check`, a successful comparison exits zero and prints its findings.
With `--check`, breaking changes **or** changes needing review exit one; unchanged
contracts and informational changes exit zero. Invalid snapshots fail either mode.
This is an explicit local CI check; it does not switch traffic, enforce platform
promotion policy or invoke tools.

## External OAuth

Before adding sensitive tools, change `auth` in `gregale-mcp.json`:

```json
{
  "mode": "external-oauth",
  "issuer": "https://identity.example.com",
  "jwks_url": "https://identity.example.com/.well-known/jwks.json",
  "resource": "https://my-mcp.gregale.dev/mcp",
  "scopes": ["mcp:tools"]
}
```

Configure the provider for OAuth 2.1, discovery, PKCE and this canonical resource
audience. The starter serves RFC 9728 protected-resource metadata and a bearer
challenge. It verifies signed RS256/ES256 JWT access tokens with issuer, audience,
expiry, subject and all configured scopes. The provider owns login, consent,
client registration and token issuance. Opaque tokens require an introspection
adapter. Endpoint scopes do not provide different tool permissions to different
clients; implement that policy before exposing such tools.

Pass an MCP client access token with `--token-env MCP_TOKEN` for authenticated
deploy verification, doctor or calls. Keep secrets out of shell history and use
`--arguments-file` for sensitive tool arguments. CLI account credentials are used
only against the Gregale control plane. Connection JSON contains no credentials;
it uses the common `mcpServers` HTTP shape, which may need adaptation for your client.
Provider/client interoperability needs qualification for your chosen provider;
this profile does not host an authorization server.

## Runtime and qualification

The starter uses the official SDK, supports MCP 2026-07-28 and optional stateless
2025-11-25 compatibility, and stops streaming work on disconnect. It logs validated
tool callback name, duration and outcome without arguments, results or tokens.
Use normal app logs to inspect those events. Stateful protocol sessions, old
HTTP+SSE and direct stdio hosting are outside this profile. For those workloads,
use an explicit adapter with durable session storage and its own acceptance tests.

Preserve dependency lockfiles. The source scanner recognizes valid npm integrity
digests without suppressing provider credential checks. Older deployed apid builds
may still reject those hashes; upgrade the scanner before qualifying reproducible
deployments. Do not disable secret scanning to bypass that issue.

Keep durable state and long-running work outside the request VM. Durable MCP Tasks,
per-customer execution isolation, gateway tool policy/metrics and tool-contract
rollout checks are follow-on capabilities, not included in this preview.

Protocol references: [Streamable HTTP](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http)
and [authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization).
Design: [ADR-426](adr/426-mcp-hosting-contract.md).

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

For HTTP failures, discovery, contract capture and calls retain the upstream
status in the JSON Problem on stderr: 401 exits 2, 5xx exits 3, and other HTTP
failures exit 1. Numeric `Retry-After` is exposed as `retry_after_seconds`;
this is retry advice, not an automatic retry. Remote error bodies are excluded
from diagnostics so HTML and credentials cannot enter the error receipt.
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
Newly visible tools are informational by default. Add `--strict-catalog` to mark
each addition as `needs_review`, so `--strict-catalog --check` rejects catalog
expansion. The receipt includes `strict_catalog: true` when enabled. This applies
even when the baseline catalog is empty; removals remain breaking. Descriptions
and other informational changes retain their existing severity.
This is an explicit local CI check; it does not switch traffic, enforce platform
promotion policy or invoke tools.

### CI checks for each caller

Capture and review one baseline per permission set. Keep reader and writer tokens
separate, and use the same issuer, audience, endpoint scopes and tool scopes for
each role's future captures. For example:

```sh
gregale mcp lock --url https://baseline.example.com/mcp --token-env MCP_READER_TOKEN --out contracts/reader.lock.json
gregale mcp lock --url https://baseline.example.com/mcp --token-env MCP_WRITER_TOKEN --out contracts/writer.lock.json
```

The reusable [catalog workflow](../.github/workflows/mcp-catalog-check.yml) builds
Gregale at a reviewed full commit SHA, checks out the caller repository's baseline
and runs only `mcp lock` and `mcp diff --check`. Strict catalog comparison defaults
to enabled. It saves the baseline, candidate and JSON receipts for seven days,
including when comparison fails. It does not execute code from the caller's
repository or invoke tools. The candidate endpoint must already be running.

Replace `REVIEWED_GREGALE_SHA` below with a full commit SHA containing this workflow
and `--strict-catalog`. Pin both the workflow and `gregale-ref` to that SHA:

```yaml
name: MCP caller catalogs
on: workflow_dispatch
permissions:
  contents: read
jobs:
  catalog:
    strategy:
      fail-fast: false
      matrix:
        include:
          - role: reader
            token_secret: MCP_READER_TOKEN
          - role: writer
            token_secret: MCP_WRITER_TOKEN
    uses: poyrazK/faas/.github/workflows/mcp-catalog-check.yml@REVIEWED_GREGALE_SHA
    with:
      gregale-ref: REVIEWED_GREGALE_SHA
      endpoint-url: https://candidate.example.com/mcp
      baseline: contracts/${{ matrix.role }}.lock.json
      receipt-name: mcp-catalog-${{ matrix.role }}
    secrets:
      endpoint-token: ${{ secrets[matrix.token_secret] }}
```

By default the baseline comes from the PR's base commit, or the caller commit for
other events. `baseline-ref` can pin another full commit SHA. Baselines must be
regular files inside that checkout. Review intentional additions before updating
a baseline; the default PR base prevents a candidate capture from replacing the
baseline used by that check. Set `legacy: true` with a separately captured 2025-11-25 baseline when that
protocol is part of the release contract. Omit `endpoint-token` for public servers.
Use explicit named secrets on trusted workflow runs; never pass production tokens
to an unreviewed Gregale pin or an untrusted candidate endpoint.

The repository's portable fixture compares the official SDK against
[reader](../testdata/mcp-catalog/reader.lock.json) and
[writer](../testdata/mcp-catalog/writer.lock.json) baselines. It verifies both
protocols, deterministic captures, unexpected reader catalog expansion, unchanged
writer visibility and writer tool removal, with zero tool calls. Catalog checks
detect changes in visibility and structure; they do not prove execution denial or
object-level authorization. Per-tool execution guards are tested separately;
object ownership remains the application's responsibility.

## External OAuth

Before adding sensitive tools, change `auth` in `gregale-mcp.json`:

```json
{
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

Configure the provider for OAuth 2.1, discovery, PKCE and this canonical resource
audience. The starter serves RFC 9728 protected-resource metadata and a bearer
challenge. It verifies signed RS256/ES256 JWT access tokens with issuer, audience,
expiry, subject and all configured scopes. The provider owns login, consent,
client registration and token issuance. Opaque tokens require an introspection
adapter. `auth.scopes` are required for every request. In the Node starter,
`auth.tool_scopes` adds application-owned permissions: every listed scope is
required in addition to the endpoint scopes. `[]` permits any authenticated
endpoint caller to use that tool. A configured map denies tools missing from it;
`{}` denies every tool. Null maps or scope arrays fail configuration validation.
Open mode allows only empty scope arrays; the generated public starter explicitly
allows its three harmless tools.

The starter filters `tools/list` using verified JWT scopes on each request and
checks `tools/call` before execution, including calls to hidden tools. Missing
tool scopes return HTTP 403 with an `insufficient_scope` bearer challenge naming
the endpoint and tool scopes; an unlisted tool returns `tool_access_denied`.
Headers, arguments and tool annotations cannot grant permissions. Register new
tools through the starter's `registerTool` helper to retain discovery filtering
and the callback guard. The JSON-RPC tool name controls authorization; an
`Mcp-Name` header does not.

Omitting `auth.tool_scopes` preserves endpoint-only authorization for existing
servers. This manifest describes application policy: deploying an arbitrary
server with this field does not install a gateway enforcement layer. Such servers
must implement the policy themselves. Object/tenant ownership checks inside each
tool remain the application's responsibility. Compare contract snapshots under
the same identity and scopes, including separate baselines for different roles.

Pass an MCP client access token with `--token-env MCP_TOKEN` for authenticated
deploy verification, doctor or calls. Keep secrets out of shell history and use
`--arguments-file` for sensitive tool arguments. CLI account credentials are used
only against the Gregale control plane. Connection JSON contains no credentials;
it uses the common `mcpServers` HTTP shape, which may need adaptation for your client.
Provider/client interoperability needs qualification for your chosen provider;
this profile does not host an authorization server.

## Runtime and qualification

The starter uses the official SDK, supports MCP 2026-07-28 and optional stateless
2025-11-25 compatibility, and stops streaming work on disconnect. Stateful protocol sessions, old
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

## Correlated execution events

New starters emit one `mcp_request` summary per MCP HTTP request, including Origin
and authentication denials before dispatch, schema failures and disconnects.
`mcp_tool_call` records only callbacks that actually execute. Both carry
`event_version: 1`, a fresh server-generated `request_id`, a recognized protocol
or `unknown`, a bounded RPC method, tool name, outcome and duration in milliseconds.
Request summaries also carry `http_status` and a fixed `reason` when available.
The response's `X-MCP-Request-ID` joins the summary to callback events and is
exposed to allowed browser origins. Caller-supplied IDs are ignored; this ID is
separate from the gateway's HTTP request ID. CLI call and discovery receipts include
`request_id` when the server returns a valid ID, ready to use with `events --request`.

```sh
gregale mcp events --app my-mcp --since 15m --json
gregale mcp events --app my-mcp --tool add --outcome denied
gregale mcp events --app my-mcp --request SERVER_REQUEST_UUID --follow
```

The command reads the existing account-authenticated application log stream;
omit `--app` inside a linked project. `--deployment` accepts a deployment ID or
`vN`; normal log plan restrictions apply. `--since` accepts a positive duration
(including `3d`) or RFC3339 timestamp. `--json` writes one validated event per line.
Tool, outcome and request filters apply locally after decoding. A retention gap,
degraded stream or error returns nonzero with an incomplete-results diagnostic;
Ctrl-C exits 130. Earlier starters' unversioned callback logs are skipped.

Outcomes are `success`, `denied`, `validation_error`, `tool_error`, `error`,
`cancelled` and `protocol_error`. A callback's success means it returned a result;
the terminal request can still fail output validation. A tool error or schema
failure can accompany HTTP 200. Requests denied before body parsing have an
unknown RPC method and no tool name. Unknown submitted tool names are bucketed as
`unknown`. Registered names must use 1–64 ASCII letters, digits, `_`, `.` or `-`.
Register new tools through the helper to preserve telemetry and authorization.

Events exclude arguments, results, tokens, identities, caller IDs and raw error
or validation messages. The CLI drops unknown fields and invalid event rows.
These are application-owned diagnostics subject to log retention and loss; they
are not a durable audit ledger or complete fleet metrics. An arbitrary customer's
server must implement the event contract itself. Existing deployed starters need
their application code updated to emit these events.

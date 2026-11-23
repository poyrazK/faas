# ADR-20261122194324: MCP gateway auth modes, per-tool insight, replay and parked input waits

- Status: Proposed (dark behind the `mcp_gateway_auth` and `mcp_tool_insights`
  platform flags; `gregale mcp server auth` and `replay` are hidden preview
  commands)
- Date: 2026-11-22
- Related: ADR-120 (consumer keys), ADR-953 (consumer plans), ADR-104
  (per-consumer throttles), ADR-1190 (Gregale Auth), ADR-1237/1238 (MCP access
  policy and gateway convergence), ADR-1256 (MCP smoke fixtures), ADR-1257
  (MCP transport observability), ADR-1700 (platform flags)

## Context

Hosted MCP servers have two access modes, `open` and `external-oauth`.
`external-oauth` asks the owner to run or rent an authorization server, then
wire issuer, JWKS and scopes into `gregale-mcp.json` and `gregale mcp policy`.
`gregale mcp server access audit` only understands `external-oauth`. Owners who
want "give this customer a key" or "let people sign in" do weeks of work for
something the platform already has the parts for:

- Consumer keys (`ck_…`, ADR-120) are verified at the edge before the JWT gate
  (`pkg/gateway/handler.go`, `enforceConsumerAuth` runs before
  `applyEdgeRuleJWT`), carry a stable consumer ID, scopes, plans (ADR-953) and
  per-consumer throttles (ADR-104). Their scopes are a closed vocabulary
  (`read`, `write`, `admin`, `billing`) checked by apid, both stores and
  `consumer_keys_scopes_vocab_chk`.
- Gregale Auth (ADR-1190) is already an OAuth 2.1 authorization server with
  dynamic client registration, PKCE and RFC 8707 resources: a client token's
  `aud` is the resource it was authorized for. Its issuer is
  `<auth app>/auth/v1`, its keys ES256 at `/auth/v1/.well-known/jwks.json`.
- The gateway MCP resource policy (`pkg/gateway/mcp_policy.go`) already answers
  protected-resource metadata, origin checks, CORS, and endpoint, tool,
  resource and prompt scopes from the JSON-RPC envelope, but only for JWTs.

Per-tool observability exists (`pkg/mcpprotocol`, dashboard MCP performance:
calls, errors, cancellations, p50/p95/p99, stream p95), but the `tool` label is
a fixed list of starter demo tools (`greet`, `add`, `stream_demo`,
`build_report`), so every customer tool is reported as `other`. There is no cost
per tool and no count of calls whose arguments did not match the tool schema.

Releases have catalog diffs, smoke fixtures and deployment comparison, but no
side-by-side check of a 0%-traffic candidate against the serving deployment
before promotion.

MCP servers on Gregale are stateless (`mode: stateless` is required). Durable
MCP Tasks already let a tool wait for input (`input_required`) with the task
state in Postgres, so the VM can park while a person answers. What is missing
is a stream a client can hold open for task and list-changed notifications
while the VM is parked.

## Decision

### 1. Gateway auth modes `api-key` and `oauth-proxy`

`gregale-mcp.json` `auth.mode` gains two values. Both are enforced at the
gateway by the existing MCP access rule (an app-bound `kind=jwt` edge rule on
`/**` with an `mcp` policy), so the audit, convergence and admission evidence
of ADR-1237/1238 apply unchanged.

**`api-key`.** Callers present a consumer key. The source lists `resource`,
endpoint `scopes` and optional tool/resource/prompt scopes, and no issuer or
JWKS. The rule's action sets `mcp.credential = "consumer_key"`. It still
carries a complete JWT shape: issuer `<API>/v1/mcp/consumer-key-issuer`,
audience = the resource, `ES256`, and `jwks_url`
`<API>/v1/mcp/consumer-key-jwks.json`, which apid serves as an empty key set
(under `/v1`, because a new root path on the API host would be reserved on
every app host too). A gateway that understands `credential` ignores these fields. An older
gateway does not know `credential`, compiles an ordinary MCP JWT policy, and
refuses every request because no token can verify against an empty key set:
it fails closed. (Leaving the JWT fields empty would fail open: old gateways
drop a JWT rule whose `jwks_url` is not `https://`.)

- Consumer-key scopes gain one namespaced form, `mcp:<scope>` (same character
  rules as MCP scopes, at most 128 bytes). The policy's scope `tools` matches a
  key scope `mcp:tools`. The CHECK constraint moves to an immutable validator
  function (expand-only: every existing row stays valid).
- A key whose scopes are only `mcp:*` passes the ADR-120 method check only on
  the MCP endpoint of a consumer-key MCP policy for that host. It cannot call
  the rest of the app.
- The gateway policy reads the consumer identity `enforceConsumerAuth` already
  verified. No key: 401 with `WWW-Authenticate: Bearer
  resource_metadata="…"`. Missing endpoint or entry scope: 403
  `insufficient_scope`, exactly as for JWTs. The metadata document lists no
  authorization server; clients send the key as a bearer header.
- The app's consumer auth mode must be `optional` or `required`; `gregale mcp
  auth enable` checks this before it writes the rule.
- **Fail closed without the policy.** In gateway-only modes the server does no
  token checks of its own, so it must not trust a request the policy did not
  see. The gateway removes any caller-supplied `Gregale-Mcp-Gateway-Authorized`
  header on every request and sets it to the rule ID only after an MCP policy
  admits the request. Starters in `api-key` and `oauth-proxy` mode answer 401
  on the MCP endpoint without it, so disabling, expiring or deleting the rule
  closes the server instead of opening it. Deploy also refuses until the stored
  policy matches the source.

**`oauth-proxy`.** Callers sign in through a Gregale Auth app. `gregale mcp
server init --auth-app` already points `external-oauth` at an Auth app, where
the server verifies tokens itself. `oauth-proxy` moves that check to the
gateway: the source names the Auth issuer (`https://<auth app>/auth/v1`), the
CLI derives `jwks_url` and writes an ordinary JWT MCP policy with audience =
the MCP resource and `ES256`. The metadata document points clients at the Auth
app, which already does dynamic registration and PKCE and binds the token
audience to the resource. Auth tokens carry only OpenID scopes (`openid`,
`email`, `profile`, `phone`), so endpoint scopes must come from that set and
per-tool, resource and prompt scopes are refused: audience binding is the
authorization.

`gregale mcp auth enable --mode api-key|oauth-proxy [--auth-app SLUG]` writes
the source change, applies the gateway policy and prints the next commands
(create a consumer key with `mcp:` scopes, or the client connection URL).
`gregale mcp server access audit` compares `api-key` and `oauth-proxy` policies
the same way it compares `external-oauth` ones.

apid refuses to create or update an edge rule whose MCP policy sets
`credential` unless `mcp_gateway_auth` is on for the account. With the flag off
nothing new is reachable.

### 2. Per-tool limits and metering

The MCP policy gains optional `tool_limits`: per tool name, a request count per
window (1 s to 1 h), applied per caller (consumer ID for `api-key`, JWT subject
otherwise) with the existing per-consumer limiter (ADR-104). Over the limit:
429 with `Retry-After`. Limits live in the policy so audit and convergence
cover them. Calls per caller are already metered by consumer usage. Per-tool
call counts are part of section 3.

### 3. Per-tool insight

This supersedes the fixed tool vocabulary of ADR-20261017191502 and
ADR-20261017191506 ("arbitrary custom tools intentionally share `other`").

- **Tool names.** A starter labels a `tools/call` with the tool's name only
  when the tool is registered in its own catalog; any other name is `other`,
  so a caller cannot choose labels. Registered names are the owner's and are
  public in `tools/list`. The collector accepts names matching
  `^[A-Za-z0-9_.:/-]{1,64}$` and keeps an app's first 16 distinct names per
  retention window; later ones are `other`. Each name adds about 45 series
  (outcomes and two histograms), so 16 bounds an app at roughly 18 tool labels.
- **Argument mismatches.** Starters report a new outcome, `invalid_params`, for
  a `tools/call` whose arguments fail the tool's input schema: the Go and
  Python starters' schema validator error, the Node MCP SDK's "Input validation
  error" tool error, or JSON-RPC `-32602` on `tools/call`. Built-in sample
  tools that validate in their handlers still report `tool_error`. It counts as
  an error and is also shown on its own. Other methods never report it (the
  SDK also answers a missing resource with `-32602`).
- **Compute per tool.** apid estimates it at read time: the tool's execution
  seconds at the app's billed memory (plan RAM + 8 MB), priced at the overage
  rate (`OverageMillicentsPerGBHour`) in integer millicents, before included
  GB-hours. Calls that overlap on one instance share its memory, so it is an
  upper bound; idle time before parking goes to no tool.
- Served on the dashboard MCP performance table behind `mcp_tool_insights`.
  Calls, errors and latency per named tool need no flag.

### 4. Replay before promote

`gregale mcp server replay --app SLUG --candidate DEPLOYMENT` runs the same
read-only probe against the serving deployment and a candidate staged with
`--no-traffic`, then diffs the two:

- the catalogs (`tools/list`, `resources/list`, `prompts/list`): added,
  removed and changed entries; a removed tool, or a new required argument, is
  breaking;
- `resources/read` for up to 32 static resources, and `prompts/get` with fixture
  arguments;
- `tools/call` only for tools named with `--allow-tool` and only with fixture
  arguments (ADR-1256 format). No production traffic is captured or stored.
- latency per method (median and maximum of 3 runs) and error outcomes.

It exits 1 on a breaking change or a new error, prints JSON with `--json`, and
writes nothing. A tool's `readOnlyHint` is never treated as permission to
call it.

### 5. Parked input waits

Not built in this change; designed in ADR-20261123003000. The direction:

- Input waits use durable Tasks (`input_required`); the VM parks between the
  client's `tasks/get` polls today. That already lets a person take minutes to
  answer without a running VM.
- A held notification stream (`subscriptions/listen`) needs the gateway to
  answer the stream from Realtime channels while the VM is parked, with the
  app publishing task and list-changed events through the Realtime publish
  API. This changes gateway stream ownership and wake rules (§6), so it gets
  its own ADR and metal evidence before any code.

## Rollout order

1. Data: the expand migrations, then the contract migration that drops the
   closed scope check. Until the contract migration runs, Postgres refuses
   `mcp:` scopes, so `mcp_gateway_auth` stays off until then.
2. Edge/node: gatewayd-internal (MCP credential, tool limits, the
   authorization mark) and vmmd (the `invalid_params` outcome and named tool
   labels). An older vmmd drops events with an unknown outcome.
3. CLI: starters scaffolded by the new CLI report `invalid_params` and need
   the mark in gateway-only modes, so publish the CLI after step 2.
4. Owner: turn on `mcp_gateway_auth` and `mcp_tool_insights` per account.

## Consequences

- Owners get key-based and sign-in-based MCP access without running an
  authorization server, using credentials, plans and limits they already
  manage.
- A new consumer-key scope form is the only schema change. It is expand-only;
  old gateways ignore `mcp:` scopes because those keys have no `read`/`write`
  scope and are refused by the ADR-120 method check, so a mixed fleet fails
  closed.
- An old gateway that receives an `mcp.credential` policy treats it as a JWT
  policy against an empty key set and answers 401: closed, not open. Release
  gatewayd-internal before turning on `mcp_gateway_auth`, or `api-key` servers
  refuse every caller.
- Per-tool series grow from 6 to at most 18 tool labels per app. Worst case
  per vmmd: 1,024 apps × 18 tools × (10 outcomes + 2 × 16 histogram series),
  about 770k series; in practice only apps with MCP traffic in the last hour
  count and few use 16 tools. A name an app stops using keeps its slot until
  the app is idle for the retention window.
- Tool limits key JWT callers by `sub`, which the MCP policy already requires.
  Buckets are per gateway process, not fleet-wide.
- The CLI keeps `mcp server auth` and `mcp server replay` out of help,
  completions and dispatch unless `FAAS_MCP_GATEWAY_PREVIEW_ENABLED` is set,
  like other dark-launched commands.
- Tool publication (platform-invoked tools) stays `open` or `external-oauth`;
  apid refuses the new modes there.

## Rejected

- **Capturing production tool calls for replay.** Arguments are customer data,
  and "read-only" cannot be known from MCP metadata.
- **A new Gregale-run authorization server for `oauth-proxy`.** Gregale Auth
  already is one, per app, with the owner's users.
- **Gateway-parsed per-tool metrics.** The gateway only sees requests when an
  MCP policy is active, and streamed responses cannot be classified without
  buffering. Starter-reported outcomes already flow through vmmd.

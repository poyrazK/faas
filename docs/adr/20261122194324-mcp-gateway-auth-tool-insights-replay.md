# ADR-20261122194324: MCP gateway auth modes, per-tool insight, replay and parked input waits

- Status: Proposed (dark behind the `mcp_gateway_auth` and `mcp_tool_insights`
  platform flags; `gregale mcp server replay` is a client-only preview command)
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

**`oauth-proxy`.** Callers sign in through a Gregale Auth app. The source names
the Auth app by its canonical origin in `auth.issuer`
(`https://<auth app>/auth/v1`); the CLI derives `jwks_url` and writes an
ordinary JWT MCP policy with audience = the MCP resource and `ES256`. The
metadata document points clients at the Auth app, which already does dynamic
registration and PKCE and binds the token audience to the resource. Version 1
is endpoint-level: Auth client tokens carry no `scope` claim, so `oauth-proxy`
refuses non-empty endpoint, tool, resource and prompt scopes until Auth issues
one. Audience binding is the authorization.

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

- **Tool names.** The collector accepts any tool name matching
  `^[A-Za-z0-9_.:/-]{1,64}$` and keeps the first 32 distinct names per app per
  retention window; later ones are `other`. This bounds series per app the way
  the app table already bounds apps. The names are self-reported by the app,
  so an app can only mislabel its own series.
- **Argument mismatches.** Starters report a new outcome, `invalid_params`,
  when a `tools/call` fails JSON-RPC `-32602` or schema validation. It counts
  as an error and is also shown on its own.
- **Cost per tool.** apid estimates it at read time. The app's billed GB-hours
  over the hour (plan RAM + 8 MB per running second, the billing rule) are
  split across tools by their share of execution seconds, then priced at the
  plan's overage rate, in integer millicents. This is labeled an estimate:
  concurrent calls share one VM, and idle time before parking goes to no tool.
- Served on the dashboard MCP performance view, `gregale mcp server
  performance`, and the management MCP, behind `mcp_tool_insights`.

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

Not built in this change. The design direction, for a follow-up ADR:

- Input waits use durable Tasks (`input_required`); the VM parks between the
  client's `tasks/get` polls today. That already lets a person take minutes to
  answer without a running VM.
- A held notification stream (`subscriptions/listen`) needs the gateway to
  answer the stream from Realtime channels while the VM is parked, with the
  app publishing task and list-changed events through the Realtime publish
  API. This changes gateway stream ownership and wake rules (§6), so it gets
  its own ADR and metal evidence before any code.

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
- Per-tool series grow from 6 to at most 33 tool labels per app.

## Rejected

- **Capturing production tool calls for replay.** Arguments are customer data,
  and "read-only" cannot be known from MCP metadata.
- **A new Gregale-run authorization server for `oauth-proxy`.** Gregale Auth
  already is one, per app, with the owner's users.
- **Gateway-parsed per-tool metrics.** The gateway only sees requests when an
  MCP policy is active, and streamed responses cannot be classified without
  buffering. Starter-reported outcomes already flow through vmmd.

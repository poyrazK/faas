# ADR-20261125230541: Upgrade prompt in MCP plan refusals

- Status: Proposed (dark: needs API monetization and an MCP resource policy)
- Date: 2026-11-25
- Related: ADR-20261124094745 (self-serve consumers, `upgrade_url`),
  ADR-20261125194510 (MCP account tools), ADR-20261123192254 (MCP per-tool
  billing and plan route limits), ADR-953 (consumer plan admission)

## Context

When a consumer's plan refuses a request, the gateway answers `429
consumer_plan_limit_exceeded` with an RFC 7807 problem that carries the
consumer's `upgrade_url` (ADR-20261124094745). MCP clients do not read that
body: a Streamable HTTP client treats a non-2xx answer as a transport error,
so the person sees "HTTP 429" and the model never learns the limit or the
link. The upgrade path built for MCP sellers is invisible exactly where it
matters.

MCP has a standard answer for "the person must open a page": error
`-32042` (URL elicitation required), whose `data.elicitations` lists
`{mode: "url", elicitationId, url, message}`. A client that declared
`elicitation.url` in its capabilities shows the page to the person with
consent.

## Decision

Owner decisions (2026-11-25): the prompt covers the monthly and per-minute
plan limits (account and per-route/per-tool); clients without URL
elicitation get a tool error that names the link; every JSON-RPC method is
answered in MCP terms. Spend-cap and overdue-invoice refusals keep their
402.

1. **Which requests.** A JSON-RPC request (an object with `id` and
   `method`) that an MCP resource policy admitted, with either credential
   (OAuth or consumer key). Notifications, batches, non-JSON bodies and
   routes without an MCP policy keep the 429 problem.
2. **Answer.** HTTP 200, `Content-Type: application/json`, `Cache-Control:
   no-store`, `X-Faas-Error-Code: consumer_plan_limit_exceeded` and the
   usual `Retry-After` and `X-Faas-Rate-Limit-Scope`. The body is one of:
   - error `-32042` with `data.elicitations[0]` = the upgrade link, when the
     client declared URL elicitation and the app has an upgrade link;
   - for `tools/call`, a result with `isError: true` whose text names the
     limit, when a per-minute limit clears, and the link;
   - for other methods, error `-32000` with the same text as `message` and
     `data` = `{code, upgrade_url, retry_after_seconds}`.
   The request is never billed (unchanged).
3. **Knowing the client's capabilities.** Protocol 2026-07-28 carries them
   on every request (`params._meta["io.modelcontextprotocol/clientCapabilities"]`).
   Older protocols declare them once in `initialize`: when that request
   declares `elicitation.url`, the gateway remembers the `Mcp-Session-Id`
   of the answer for 24 hours, per app, in that gateway process (at most
   1,024 sessions per app, so one app's callers cannot crowd out another's;
   a full app drops expired sessions, then an arbitrary one). A later
   request of the session reaching another node gets the tool-error form,
   which still carries the link.
4. **Ownership.** Only gatewayd-internal changes. apid still mints the link
   (`Auth.ConsumerUpgradeLink`).

## Compatibility

- No schema, proto or API changes. An older gateway answers 429 as before.
- Clients that relied on the 429 for MCP requests now get HTTP 200 with a
  JSON-RPC error or tool error; MCP clients could not act on the 429.
- Request telemetry records the answer as status 200 with the platform
  error code header, so it counts as a gateway answer, not the app's. MCP
  telemetry (`mcp_requests` outcome, seller insights) still counts it as
  `rate_limited`.
- A request whose body spells `id`, `method` or `params` twice (in any
  case) keeps the 429, so the answer never echoes an id the app would not
  read.

## Consequences

- An agent past its plan's limit can tell the person how to upgrade, and a
  client with URL elicitation offers the page directly.
- The gateway reads MCP request bodies once more (bounded by
  `api.MCPPolicyMaxRequestBytes`, already read by the policy).
- Session memory is per node; multi-node deployments fall back to the
  tool-error form for some requests of older-protocol sessions.

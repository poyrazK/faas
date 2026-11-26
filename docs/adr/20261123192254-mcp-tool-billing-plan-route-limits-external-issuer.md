# ADR-20261123192254: MCP per-tool billing, plan route limits and external OAuth issuers

- Status: Proposed (MCP parts dark behind the `mcp_gateway_auth` platform flag
  and the hidden `gregale mcp server auth` / `keys` preview commands; plan
  route limits ship with consumer plans)
- Date: 2026-11-23
- Related: ADR-952 (consumer route weights), ADR-953 (consumer plans),
  ADR-120 (consumer keys), ADR-1237/1238 (MCP access policy),
  ADR-20261122194324 (MCP gateway auth modes)

## Context

ADR-20261122194324 let MCP owners hand out consumer keys (`api-key`) or let
people sign in through Gregale Auth (`oauth-proxy`). Three gaps remain before
an owner can sell an MCP server:

1. Every MCP request is one HTTP route (`POST /mcp`). Route weights and plans
   price and cap routes, so a cheap `tools/list` and an expensive
   `tools/call generate` cost the same, and protocol chatter (`initialize`,
   `notifications/*`, `ping`, list calls) uses the consumer's quota.
2. Plans cap requests per minute and units per month for the whole app. An
   owner cannot say "free plan: 20 `generate` calls a month".
3. `oauth-proxy` works only with Gregale Auth. Owners with an existing IdP
   (Auth0, Entra, Okta, Keycloak) must drop to `external-oauth` and lose the
   gateway's scope checks before their code runs.

## Decision

**Per-tool billing labels.** After the MCP policy parses a JSON-RPC body, the
gateway replaces the request's billing route label with an MCP label:

| Method | Label |
|---|---|
| `tools/call` of a tool listed in `tool_scopes`/tool limits | `MCP /tools/<name>` |
| other `tools/call` | `MCP /tools` |
| `prompts/get` of a listed prompt | `MCP /prompts/<name>` |
| other `prompts/get` | `MCP /prompts` |
| `resources/read` | `MCP /resources` |
| anything else | protocol overhead (no label) |

Names are labelled only when the owner's policy lists them and they match
`[A-Za-z0-9_.-]{1,128}` (`api.MCPBillingNameMaxBytes`), so a caller cannot
mint unbounded label cardinality. Plan admission keeps a second, unbounded
label per request (the declared or inferred route, or `MCP /tools/<name>`
for any valid tool name): plan route limits and plan unit weights look
routes up by it, so a full billing label set, or a tool the policy does not
list, cannot switch a cap off. It never enters the bounded set or the usage
ledger. Labels reuse the existing per-app label set
(first 50 labels, ADR-952), the existing `route_weights` on rate cards and the
existing usage tables; no billing schema changes.

**Protocol overhead is free.** Overhead requests are unbillable (the same path
as gateway refusals) and count 0 units toward a plan's monthly cap. They still
count toward per-minute admission, so a client cannot flood `ping`.

**Plan route limits.** `api_consumer_plans.route_limits` (jsonb, `{label:
{max_requests_per_minute, max_requests_per_month}}`, ≤50 entries, `0` =
unlimited) caps one route per consumer. Counters live in
`api_consumer_plan_route_admissions` keyed by (consumer, route), locked after
the consumer's plan admission row, and are written only when both the route
and the plan admit the request. A plan with only route limits takes no
counter for routes it does not cap. A refusal is a `429` with
`x-faas-rate-limit-scope: consumer-plan-route-minute|month`, the limit and the
observed value. The CLI takes `--route-limit "LABEL=N/minute,N/month"` and the
MCP shorthand `--tool-limit NAME=...`; rate cards take `--tool-weight NAME=W`.

**oauth-proxy with any OIDC issuer.** `gregale mcp server auth enable --mode
oauth-proxy --issuer URL [--audience AUD]` discovers the JWKS from the
issuer's OpenID Connect discovery document or RFC 8414 metadata. The issuer
must be https, the document must name exactly that issuer, and its `jwks_uri`
must be https. `gregale-mcp.json` stores the issuer, JWKS and optional
audience (default: the resource); the gateway JWT rule pins all three and
accepts RS256 and ES256. No gateway change: JWT rules already accept any
issuer. An audience other than the resource (Entra issues `api://…`
audiences) is declared on the policy as `MCPResourcePolicy.audience`;
apid accepts a rule whose JWT audience is exactly the resource or exactly
that declared value, for bearer-token policies only. This narrows the
RFC 8707 resource binding of ADR-644 to an owner-declared value, never to
an arbitrary one.

**Keys for named tools.** `gregale mcp server keys create --tools a,b` grants
the endpoint scopes plus each tool's scopes from `gregale-mcp.json`, and fails
for a tool not listed there. Keys carry scopes, not tool lists, so it also
refuses when those scopes would unlock another scoped tool that was not
asked for, and names it.

## Consequences

- Owners price and cap tools with the plan and rate-card machinery they
  already have; statements, quotes and alerts show MCP labels like any route.
- Unlisted tools share `MCP /tools` on statements, so pricing a tool on a
  statement requires listing it; plan caps work either way.
- MCP protocol overhead is unbillable, so when the usage outbox append fails
  it is kept out of the debugger fallback ledger (which bills every row),
  like gateway refusals: its request count is lost in that incident path
  rather than billed.
- A plan with route limits adds one row lock per limited request; plans
  without route limits take the old path.
- External issuers are trusted for their whole key set; rotating keys at the
  IdP needs no Gregale change, moving the issuer URL needs `auth enable` again.

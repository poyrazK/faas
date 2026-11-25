# ADR-20261125194510: Account tools in sold MCP servers

- Status: Proposed (dark: needs `mcp_gateway_auth`, API monetization and an
  app opt-in)
- Date: 2026-11-25
- Related: ADR-20261124094745 (self-serve consumers), ADR-20261123192254
  (MCP per-tool billing and plan route limits), ADR-20261122194324 (MCP
  gateway auth modes), ADR-849 (consumer usage)

## Context

ADR-20261124094745 lets a stranger sign in to an owner's MCP server, become
a consumer on a free plan and get an upgrade link when a limit refuses a
call. Someone who signed in with OAuth has no key, so the consumer
self-service API (`/v1/consumer/...`, which authenticates with a consumer
key) is closed to them. Their only view of their account is the
`upgrade_url` in a 429, and only after they hit the limit. Their agent
cannot answer "how much have I used?" or "what would Pro cost?".

## Decision

Owner choices (2026-11-25): the gateway adds the tools to the server's own
tool list (no second server for the buyer to add); the tools read usage and
limits, and list plans with the upgrade link (no plan change, no closing);
the owner opts in.

1. **Opt-in.** `api_consumer_signup_settings.account_tools`, set with
   `gregale api consumer signup set --account-tools`. It needs
   `--oauth-consumers` (CHECK constraint and API validation).
2. **Two tools, answered by the gateway.**
   - `account_usage`: the caller's plan and its limits (requests per
     minute, units per month, per-tool caps), this UTC month's requests and
     units, units left, and requests and units per tool. Usage comes from
     the consumer's usage buckets, so it trails live traffic by about a
     minute.
   - `account_plans`: the plans the owner offers with their prices, which
     one the caller is on, and, when the owner lets customers change plans,
     a fresh `upgrade_url` (the ADR-20261124094745 hosted page; a person
     saves a card there).
   Both take no arguments, are read-only (`readOnlyHint`) and return the
   answer as JSON text plus `structuredContent`.
3. **Who sees them.** Callers the gateway attributed to a consumer through
   OAuth enrollment, on an app with the opt-in. apid adds
   `account_tools` to `EnrollOAuthConsumerResponse`; the gateway caches it
   with the caller's consumer (five minutes, as the consumer itself).
   Consumer-key callers keep the key-authenticated self-service API.
4. **Listing.** For those callers, a `tools/list` request without a cursor
   (the first page) has the two tools appended to `result.tools` of the
   app's answer, JSON or a `text/event-stream` event. The gateway buffers
   at most 1 MiB of a JSON answer; a larger answer, an error answer or an
   answer it cannot parse passes through unchanged. The names are reserved
   while the opt-in is on: an app tool with the same name is dropped from
   the list and its calls reach the gateway's tool.
5. **Calls.** A `tools/call` of either name is answered by the gateway
   once every matching jwt rule admitted the caller (including its
   customer-claim check), before tool scopes, tool limits and plan
   admission: a caller past its plan's limit can still ask
   for the upgrade link. The call never reaches the app and is not metered
   or billed. A per-consumer limit of 30 account-tool calls a minute
   (`api.MCPAccountToolCallsPerMinute`), counted on each gateway node,
   bounds the apid work; past it the answer is a tool result with
   `isError: true` (HTTP 200), which the model can read. The gateway reads the answer from apid's new
   `Auth.ConsumerAccountTool` RPC (InvalidArgument, NotFound,
   FailedPrecondition when the opt-in is off, Unavailable); the RPC returns
   the tool's JSON.
6. **Ownership.** apid stays the only reader of plans, prices and usage for
   this; the gateway only relays its answer and edits the tool list.

## Compatibility

- The column is expand-only with a default; older apid builds ignore it.
- `account_tools` is a new proto field: an older apid never sets it, so
  newer gateways add nothing; an older gateway ignores it.
- A newer gateway calling an older apid gets Unimplemented and answers the
  tool call with a tool result with `isError: true`.
- Deploy order: migration, apid, then gatewayd-internal.

## Consequences

- An agent can see its own usage and the way to upgrade without the owner
  writing code, and before it hits a limit.
- The gateway edits an application's JSON-RPC answer for the first time.
  The edit is limited to `tools/list` answers to opted-in callers, appends
  only, and leaves anything it cannot parse alone.
- Not covered: consumer-key callers' tool lists, changing or closing plans
  from a tool, and later pages of a paginated tool list.

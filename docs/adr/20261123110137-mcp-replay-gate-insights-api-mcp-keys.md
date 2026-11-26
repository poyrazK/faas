# ADR-20261123110137: MCP replay promotion gate, tool insights API and MCP keys

- Status: Accepted (built dark)
- Date: 2026-11-23
- Related: ADR-20261122194324 (gateway auth, tool insights, replay),
  ADR-120 (consumer keys)

## Context

ADR-20261122194324 shipped three pieces that each stopped one step short of
being useful on their own:

- `gregale mcp server replay` compares a zero-traffic candidate with the
  serving deployment, but only when someone runs it by hand. The deploy path
  promotes without it.
- Per-tool calls, argument mismatches and estimated compute exist only on the
  dashboard's MCP operations page. Agents and scripts use the CLI and API.
- api-key mode admits consumer keys with `mcp:<scope>` scopes, but
  `gregale api consumer key create` refused those scopes on the client, so
  the command `mcp server auth enable` printed as the next step failed.

## Decision

1. **Replay gate on deploy.** `gregale mcp server deploy --replay` runs
   replay after the smoke fixtures and before the traffic switch. It reuses
   the prompt fixtures in `--fixtures` and never calls a tool:
   `--allow-tool-execution` consents to one call on the candidate, not to
   repeated calls on the serving production deployment, so tool fixtures are
   reported as skipped (manual `mcp server replay --allow-tool` names
   production tools explicitly). On an authenticated server it needs
   `--token-env`, because both deployments must answer. A breaking catalog change, a probe
   that newly fails, or an unreadable deployment leaves the candidate
   unpromoted, and the receipt carries the replay report. A first deployment
   has nothing to compare with and passes with `skipped`. The flag is opt-in
   and works only in the preview (`FAAS_MCP_GATEWAY_PREVIEW_ENABLED`).
2. **Tool insights API.** `GET /v1/apps/{slug}/mcp-tool-insights` returns
   the last hour of per-tool calls, errors, cancellations, mismatches,
   execution seconds, GB-seconds, an estimated cost and latency percentiles.
   It reads the same queries as the dashboard (`mcpprotocol.Fetch`), is gated
   by the `mcp_tool_insights` flag (403 when off), and reports
   `telemetry: not_observed` instead of an error when nothing was reported in
   the window. Cost is integer millicents and an upper bound at the overage
   rate. `gregale mcp server insights` prints it.
3. **MCP keys.** `gregale mcp server keys create|list|revoke` issues consumer
   keys with `mcp:` scopes. Scopes are given without the namespace and default
   to the endpoint scopes in an api-key `gregale-mcp.json`; `--new-consumer`
   creates the API consumer in the same step. `api consumer key create` now
   also accepts `mcp:<scope>`. The server already validated these scopes
   behind the `mcp_gateway_auth` flag; nothing changes server-side.

## Consequences

- No migration and no gateway change; the release order of
  ADR-20261122194324 still applies before the flags are turned on.
- Replay adds up to two minutes to a gated deploy. It sends read-only
  discovery, static resource reads and reviewed fixtures only, so the
  serving deployment sees a handful of extra requests per deploy.

## Rejected

- **Replay on by default.** A deploy that used to promote would start
  refusing on catalog changes the owner already accepted. It stays opt-in
  until prod evidence shows the gate's noise floor.
- **A separate keys API.** Consumer keys already carry the scopes; a second
  surface would duplicate revocation and audit.

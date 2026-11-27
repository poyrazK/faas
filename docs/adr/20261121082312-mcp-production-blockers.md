# ADR-20261121082312: Hosted MCP reads logs through the public edge and admits native-client callbacks

Date: 2026-11-21
Status: Accepted
Amends: ADR-20261026195952 §2 (the hosted bridge calls apid on loopback),
ADR-20261030065746 (redirect URI policy)

## Context

A production check of the management MCP server on rc.360 (2026-11-20/21)
found ten blockers. Eight were plain defects fixed without a decision (a
parked app's logs, workflow attention, oversized forecast, schema and
context results, idle eviction of in-memory plans, redeploying a linked
directory, the Node MCP starter's protocol header). Two need one:

1. **Hosted logs.** The hosted bridge (`mcp-bridged`) sends every request to
   apid's loopback listener. App log streams (`/v1/apps/{slug}/logs`) are
   compute-owned: the public edge sends them to the compute gateway, and apid
   does not serve them. `gregale_get_logs` answered 404 "no such API route"
   on every hosted connection, so claude.ai, ChatGPT and other cloud agents
   could never read logs.
2. **Native clients.** `gregale mcp connect` configures Cursor and VS Code for
   OAuth sign-in, but the bridge accepted only HTTPS callbacks and loopback
   `/callback` paths. Cursor returns through the private-use scheme
   `cursor://anysphere.cursor-mcp/oauth/callback`, which no configuration
   could list; VS Code's `https://vscode.dev/redirect` and its loopback root
   were not listed.

## Decision

### 1. Log streams use the public API origin

The bridge gains `LogsAPIBase`, the origin for app log streams (live and
archived). The hosted bridge defaults it to its own public origin
(`https://api.<platform>`), so log reads take the same path as the CLI:
edge, compute gateway, with the connection's own credential. Every other
call stays on loopback. A self-hosted `gregale mcp serve` keeps its single
API base.

### 2. Exact private-use scheme callbacks

An operator may list an RFC 8252 §7.1 private-use scheme callback with
`--oauth-redirect-uri`. It matches exactly, like an HTTPS callback; there is
no wildcard. `javascript`, `data`, `file`, `vbscript`, `blob`, `about`,
`intent` and the web schemes are never private-use. PKCE still binds the
code to the client that started the flow, and consent shows the return
address. The packaged bridge lists Cursor's callback, VS Code's
`https://vscode.dev/redirect`, and the loopback root on any port.

Windsurf is not listed: its callback is unverified.

## Consequences

- Hosted log reads leave the control plane and come back through the edge,
  one hop more than other reads.
- Cursor and VS Code can complete hosted sign-in.
- Flag, budget and reviewed plans still live only in memory, like other
  plans holding temporary inputs: idle cleanup now keeps them until they
  expire, but a bridge restart (a release) drops them and the agent plans
  again.

## Alternatives considered

- **Serve app logs from apid.** Moves compute-owned streaming into the
  control plane; the edge split exists to keep it out.
- **Allow any private-use scheme a client registers.** Any local app could
  then claim to be any client's return address; listing exact callbacks
  keeps the operator's allowlist the boundary.

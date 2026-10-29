# ADR-20261029085359: Headless agents open the hosted MCP server with agent keys

Date: 2026-10-29
Status: Accepted
Amends: ADR-20261026195952 (hosted management MCP: who can connect)
Related: ADR-20261026174727 (secret entry and provisioning), IAM-5 (key expiry)

## Context

The hosted management MCP server at `https://api.<platform domain>/mcp`
accepted only its own OAuth tokens. An agent without a person at a browser,
such as one in CI or a scheduled job, had to run the local `gregale mcp serve`
with a key instead. Almost every other platform's hosted MCP server takes an
API key or personal token as the bearer token; Stripe goes further and accepts
only keys made for agents.

Every Gregale key could become an MCP credential if the server simply took
keys. That would turn each CI deploy key into an agent key too.

## Decision

### 1. An `agent` scope marks keys made for agents

`agent` joins the scope vocabulary (the database check becomes
`api_keys_scopes_vocab_v3_chk`, replaced online as before). It grants no API
route by itself. Creating a key with it:

- refuses `admin` alongside it;
- adds `apps:read` if missing, since every MCP tool starts from inspection;
- sets a 90-day expiry by default (`api.AgentKeyLifetime`), shorter than the
  365 days other non-admin keys get, because agents run unattended.

`gregale key create ci-agent --scopes agent,deploy:write` makes one; rotation
and revocation work as for any key.

### 2. The hosted server takes agent keys as bearer tokens

`mcp-bridged` enables `HTTPOptions.AgentKeys`. A bearer token that is not one
of the bridge's OAuth tokens and has an API key's shape is checked with
`GET /v1/account`, which now returns `key_scopes`, the presenting key's
scopes (empty for dashboard sessions). The bridge refuses with 403
`insufficient_scope` and a pointer to agent keys any key that lacks `agent`,
carries `admin`, or lacks `apps:read`. An unknown key gets the usual OAuth
challenge. Self-hosted bridges keep refusing keys unless they opt in.

### 3. The key's scopes stand in for consent

| Key scope | Tools |
|---|---|
| `agent` + `apps:read` | Inspection |
| `deploy:write` | Deploys by upload, promotion, rollback, domains, crons |
| `secrets:request` | Secret requests (values still go only through a Gregale page) |
| `storage:manage` | Buckets |
| `postgres:manage` (+ `postgres:read`) | PostgreSQL |

A key reaches every app and environment in its account, like a local
connection; apid enforces its scopes on every call as for any key. Unlike a
local connection, it gets no local-directory deploy or env tools: the hosted
server cannot see the agent's files, so code arrives through the upload tools.

### 4. Key connections keep nothing durable

The bridge keeps one connection per key hash, built on first use and dropped
after ten idle minutes like an OAuth grant's server, within the same 2,000
bound. Its release plans live in memory; a bridge restart drops them and the
agent plans again. Every request re-verifies the key, so a revoked or
expired key stops at its next request. `gregale_management_mcp_oauth_entries`
counts them as `kind="agent_keys"`.

## Consequences

- CI and scheduled agents use the hosted server with no browser and no CLI
  installed.
- A leaked CI deploy key still cannot drive the agent tools; a leaked agent key
  can, within its scopes, until it is revoked or expires.
- `GET /v1/account` grows an optional `key_scopes` field; the SDKs are
  regenerated.
- An agent key's connection does not appear in `gregale mcp connection list`,
  which lists OAuth grants; revoking the key ends it.

## Not included

- Limiting an agent key to named apps. The key reaches its account; a per-app
  list can follow if CI agents need it.
- Making agent keys unusable on the REST API.

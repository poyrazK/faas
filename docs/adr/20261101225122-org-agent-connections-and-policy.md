# ADR-20261101225122: Agents connect to one organization, and organizations set their agent policy

Date: 2026-11-01
Status: Accepted
Amends: ADR-20261030133644 (connected agents), ADR-20261028205920 (hosted MCP sign-in)
Related: ADR-20261029085359 (agent keys), ADR-20261026195952 (hosted management MCP)

## Context

A hosted MCP sign-in mints an `mcp-login` key bound to the person's personal
organization. Gregale refuses a personal-organization key on any app of a
shared organization, so an agent a team member connects cannot reach the
team's apps at all. Teams also have no say over their members' agents: an
owner can't see which agents act in the organization, revoke them, or keep
agents from deploying.

Organization keys already do most of what is needed. A key bound to a shared
organization reaches only that organization's apps, and every request is
limited by the minting member's current role (a viewer's key cannot deploy;
a removed member's key stops working). Owners and admins already list and
revoke the organization's keys.

## Decision

### 1. A connection acts in one organization

Gregale's approval page for a hosted MCP sign-in lists the person's personal
organization (the default) and every shared organization where their role
can see apps (owner, admin, developer, viewer) and whose policy allows
agents. The code remembers the chosen organization (`cli_auth_codes.org_id`).
For a shared organization, the exchange mints the connection's `mcp-login` key
as an organization key (`CreateOrgAPIKeyWithProvenance`, recorded in the
organization's activity). The key keeps sliding like any `mcp-login` key. To
act in another organization, the person connects again.

`GET /v1/account` names the key's shared organization (`key_org`), and the
hosted MCP server then lists that organization's apps
(`GET /v1/orgs/{slug}/apps`) for consent and its tools. App routes already
accept any app of the organization within the member's role. Consent names
the organization the connection acts in.

### 2. Organizations set an agent policy

`org_agent_policies` holds one optional row per shared organization:

- `agents_allowed`: whether hosted MCP connections and agent keys may act in
  the organization;
- `allowed_scopes`: the capabilities they may carry, a subset of the grantable
  hosted MCP scopes (`deploy:write`, `secrets:request`, `storage:manage`,
  `postgres:manage`). `apps:read` is always allowed.

No row means agents are allowed with every capability, as today.
`GET /v1/orgs/{slug}/agent-policy` is open to members. `PUT` needs an owner or
admin, MFA, and is audited in the organization's activity.

The policy is enforced where an agent's credential is made:

- the approval page offers only organizations that allow agents, and shows
  the capabilities the policy leaves;
- the exchange checks the policy again and mints only the scopes the policy
  allows (requested scopes ∩ `allowed_scopes`, plus `apps:read`), or refuses
  when agents are not allowed;
- an organization key with the `agent` scope can be created only within the
  policy.

The hosted MCP server offers at consent only the capabilities the key
actually carries (`key_scopes`), so a narrowed connection never shows a tool
it can't use.

Tightening the policy revokes, in the same request, every agent connection
of the organization that exceeds it: all of them when agents are no longer
allowed, otherwise each one carrying a scope outside the new ceiling. The
response counts them.

### 3. Owners and admins see and revoke the organization's agents

`GET /v1/orgs/{slug}/agent-connections` lists the organization's agent
connections (its `mcp-login` keys and `agent`-scope keys) with the member who
connected each. `DELETE /v1/orgs/{slug}/agent-connections/{id}` and
`POST /v1/orgs/{slug}/agent-connections/revoke_all` revoke them. All three need
an owner or admin; revocation goes through the organization key revocation,
so it is audited in the organization's activity. Each member still lists and
revokes their own agents with `/v1/agent-connections`, which now names the
organization each acts in.

CLI: `gregale mcp connection list|revoke --org <slug>` and
`gregale org agent-policy get|set`.

## Consequences

- Team members' agents reach the team's apps, within their role, and only
  where the team allows agents.
- An owner can switch agents off for the organization and cut every agent off
  at once.
- Connections made before this change stay bound to the personal
  organization.

## Not included

- One connection across several organizations: keys are bound to one
  organization, and a multi-organization credential is a larger change.
- A shorter maximum connection lifetime per organization.
- Limiting agents to some of the organization's apps by policy; the person
  still picks apps at consent.

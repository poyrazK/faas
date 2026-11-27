# ADR-20261127160000: The Connect consent approves an agent's deploys

Date: 2026-11-27
Status: Accepted (owner decision 2026-11-27)
Amends: ADR-20261104140058 (human approval for risky agent actions),
ADR-20261107140000 (agent approval notifications), ADR-20261112080601 §4
(undo always asks)
Related: ADR-20261026195952 (hosted management MCP), ADR-20261028205920
(agent sign-in), ADR-20261111114059 (agent budgets), ADR-1242 (protected
environment review)

## Context

A person connects an agent (ChatGPT, Claude, Cursor, …) to Gregale on the
Connect screen: they sign in, choose "Deploy and manage apps", approve the
sign-in on Gregale's page, and tick "Also allow reviewed deployments,
production promotion, checked rollback, restarts, scaling, and domain/cron
changes". The connection's `mcp-login` key then gets every approval category
(ADR-20261104140058 §3), so the first production deploy asks the same person
again: a `428 approval_required`, a review link, a fresh sign-in, Approve,
and a retry. A rollback asks again, and so does every later deploy.

In the ChatGPT plugin test on 2026-11-27 one deploy took several ChatGPT
"Allow" prompts plus Gregale approvals. The person had just said yes to
exactly these actions on the Connect screen. Asking again adds no
information, and an agent connection cannot be used without someone at the
review page.

## Decision

### 1. The Connect consent is the approval

When the person grants deploy access on the Connect screen, that grant is
their approval of the connection's deploys, promotions, checked rollbacks,
restarts and undo in production. apid lets those requests through without
opening an approval request, without a `428`, and without notifications.

### 2. How the grant reaches apid

The grant must come from the person, never from the agent or the bridge
alone, so it rides on the sign-in code the person approves:

- A new sign-in code scope, `approvals:connect`, takes bit 7 of the code's
  scope field (`api.MCPCodeScopes`: the seven `MCPGrantableScopes`, then
  this one; a later grantable scope takes bit 8). It is not in
  `MCPGrantableScopes`, so organization agent policies neither list nor cap
  it; an organization limits it with its approval floor (§4). It is never a
  key scope: the `api_keys` scope vocabulary does not include it, and key
  creation refuses it.
- The bridge asks for it whenever the person chooses deploy access on the
  Connect screen. apid's sign-in approval page (`/cli-auth`, signed in, with
  the recent-sign-in check of every MCP approval) lists it: "deploy,
  promote, roll back, restart and undo its changes in production without
  asking you again each time; each is recorded in your audit log".
- When apid exchanges an approved code that carries it, it mints the
  `mcp-login` key without it and records the grant in
  `api_key_connect_grants (key_id PK → api_keys ON DELETE CASCADE,
  granted_by account, granted_at)`: who approved the sign-in, and when.
  apid is the only writer. Revoking or expiring the key ends the grant.
- An apid that predates the scope refuses the code; the bridge then asks
  again without it, and the connection keeps today's approvals.

No other credential gets a grant: agent keys, CLI sign-ins, API keys and
deploy tokens keep today's behaviour.

### 3. What the grant covers

Exactly what the Connect screen names, route by route
(`agentapproval.Rule.Connect`):

| Consent | Requests |
|---|---|
| Reviewed deployments | `POST /v1/apps/{slug}/deployments`, `…/deployments/source-ref`, `…/deployments/source-tarball`, an upload commit (`POST /v1/uploads/{id}/commit`), `POST /v1/deployments/{id}/retry`, and `PATCH /v1/apps/{slug}`, through which a deploy applies its `gregale.yaml` settings |
| Production promotion | `PATCH /v1/deployments/{id}/traffic`, `POST /v1/deployments/{id}/promote`, `…/promote-with-application-ack`, `…/canary/advance` |
| Checked rollback | `POST /v1/apps/{slug}/rollback` |
| Restarts | `POST /v1/apps/{slug}/restart` |
| Domain and cron changes | `DELETE /v1/domains/{domain}`, `DELETE /v1/domains/{domain}/hosts`, `DELETE /v1/crons/{id}` (adding and changing them never asked) |
| Configuration (the `env:write` choice) | `PUT /v1/apps/{slug}/env/{key}`, `POST /v1/apps/{slug}/env/batch`, `POST /v1/apps/{slug}/config/rollback` |
| Undo of the agent's own changes | `POST /v1/agent-connections/self/undo` |

Everything else keeps asking: other deletes, credentials, billing, secrets,
run-command tasks, flag and experiment publishes, dead-letter replays,
workflow recovery, edge rules, and project or environment releases
(promotions, release sets, GitOps). The Connect screen does not name them;
widening the grant to any of them needs the owner's decision and an
amendment here.

### 4. Hard limits stay

The grant waives a person's per-request approval and nothing else:

- An organization's approval floor (ADR-20261104140058 §3) still applies:
  a covered request in a floor category asks as before. With `production`
  in the floor, undo asks too.
- A key whose categories a person chose (`gregale key create
  --approvals`) follows that choice; the grant only replaces the default of
  "every category". Sign-in keys have no chosen categories today, so this
  guards a later way to choose them.
- Agent budgets and their `budget` approvals (ADR-20261111114059), spend
  caps, plan limits and quotas are unchanged.
- Protected environments keep their own human-issued approval (ADR-1242).
- Scopes are unchanged: the grant never lets a key do what its scopes do not
  allow, and the bridge still exposes only the tools the consent selected.
  There is no app-deletion tool.

### 5. Audit trail

Every request the grant lets through writes an audit event
`agent_approval.connect_grant` to the account that granted it, with the key,
the category, the method and path, the organization, and the grant's
`granted_by` and `granted_at` from `api_key_connect_grants`. The agent activity row of the
write is recorded as before (ADR-20261107220000), so the connection's
activity and the audit event name the same key and path.

### 6. The Connect screen

Every permission choice on the Connect screen starts ticked: the deploy
access choice, write access with its consent, the default environment, the
optional capabilities, all apps, app diagnostics, regression triage and the
named environments. The person unticks what they do not want. When the
grant is present, the write consent says that Gregale will not ask again for
each one and that each is recorded in the audit log.

### 7. Notifications

A request the grant lets through opens no approval request, so
ADR-20261107140000 sends no email, channel message or webhook for it.

## Consequences

- A hosted MCP connection (ChatGPT, Claude, Cursor) deploys, promotes and
  rolls back a production app with no Gregale approval step. The client's
  own confirmation prompts ("Allow ChatGPT to use …?") are the client's and
  stay; Gregale keeps its tool annotations truthful.
- Connections made before this change keep today's approvals until they
  reconnect.
- A stolen `mcp-login` key with the grant deploys without a person. It is
  held only by the hosted bridge and was already able to deploy
  non-production apps alone; revoking the connection stops it.

## Alternatives considered

- **Set the key's approval categories to "none but credentials, billing and
  delete" at sign-in.** Reuses `api_key_approval_categories`, but waives the
  whole `production` category, which covers far more than the Connect screen
  names (tasks, flags, replays, workflow recovery, project releases), and
  records no trace of why a request went through.
- **Let the bridge tell apid after its own consent page.** The bridge holds
  the key, so any holder of an `mcp-login` key could then waive its own
  approvals. The person's approval of the sign-in code is the only step the
  agent cannot perform.
- **Mark write tools `readOnlyHint` so ChatGPT stops asking.** Untruthful;
  OpenAI's review checks annotations.

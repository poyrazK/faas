# ADR-20261104085712: Agent connections can be limited to projects

Date: 2026-11-04
Status: Accepted
Amends: ADR-20261101225122 (org-bound agent connections), ADR-20261029085359 (agent keys)
Related: ADR-20261030133644 (connected agents), ADR-20261026195952 (hosted management MCP)

## Context

An agent connection reaches every app its key reaches: every app of the
account, or of the organization the connection acts in, within the member's
role. A team that connects an agent to work on one service hands it the
whole organization. People asked to point an agent at one project.

Gregale already binds one kind of credential to less than an account: a
per-app deploy token carries an `AppID`, and `appaccess.authorize` and
`LoadApp` refuse any other app with the same 404 a cross-account probe gets;
the authentication middleware refuses its routes outside `/v1/apps/{slug}`.

## Decision

### 1. A key may be limited to projects

An agent key (`agent` scope) or a hosted sign-in's `mcp-login` key may carry
a set of projects of its organization. A limited key reaches only the apps
whose `project_id` is in the set, including apps added to those projects
later. An app outside every project is outside the limit. The limit is part
of the key: apid enforces it for every client that uses the key, not only
the hosted MCP server.

Storage: `api_key_project_limits (key_id PK → api_keys ON DELETE CASCADE,
project_ids uuid[] NOT NULL, created_at)`. A row means the key is limited; no
row means it is not. Deleting a project leaves its id in the array, where it
matches nothing, so a limit never widens into "all projects". The set is
fixed when the key is minted; to change it, connect again or mint a new key.

### 2. Enforcement fails closed

When it authenticates an agent or `mcp-login` key, the middleware loads its
limit (one primary-key read) onto the principal.

- **Apps.** `appaccess.authorize` and `LoadApp` refuse an app outside the set
  with the 404 they give a cross-account app. This covers routes that address
  the app by path and those that resolve it from the body or a stored session
  (uploads, domains, crons, deployment reads).
- **Projects.** `/v1/projects/{slug}` routes refuse a project outside the set
  (404).
- **Lists.** `GET /v1/apps`, `GET /v1/orgs/{slug}/apps` and
  `GET /v1/projects` return only what the set allows.
- **Everything else.** A limited key reaches only an allowlist: the app,
  project and list routes above, `/v1/deployments/{id}`, `/v1/uploads`,
  `POST /v1/domains`, `POST /v1/crons`, `GET /v1/account`,
  `GET /v1/capabilities` and `/v1/agent-connections/self`. Any other route
  answers 403 `insufficient_scope` ("Key limited to projects"), as an
  app-bound deploy token does outside its app. New routes stay closed until
  someone adds them deliberately.

Managed Postgres databases belong to the organization, not to a project, so a
limited key cannot hold `postgres:manage` or `postgres:read`: minting drops
them, and the consent page does not offer them.

### 3. People choose projects when they connect

The hosted MCP approval page keeps "All projects" as the default for the
chosen account or organization, and lists that organization's projects to
narrow it. The code remembers the choice (`cli_auth_codes.project_ids`); the
exchange checks each project belongs to the connection's organization and
mints the limited key. The hosted MCP server's consent page and
`mcp connection list` name the projects.

### 4. Headless agent keys take the same limit

`gregale key create --scopes agent --projects api,web` and
`gregale org keys add --scopes agent --projects …` mint limited keys
(`POST /v1/keys` and `POST /v1/orgs/{slug}/keys` take `projects`, a list of
project slugs, only with the `agent` scope). Key and connection responses
return `projects`.

## Consequences

- An owner can give an agent one service without giving it the organization.
- A limited key cannot manage databases or reach account-level routes, even
  read-only ones; the allowlist grows only by review.
- Each request of an agent or `mcp-login` key costs one extra primary-key
  read.

## Not decided here

- Limiting by environment within a project.
- Changing a connection's projects without reconnecting.
- Limits for keys other than agent and `mcp-login` keys.

## Amendment 1 (2026-11-04): every key path honours the limit

The limit was enforced only by apid's API middleware. Three other paths
authenticate the same keys and now refuse what the API refuses:

- **The gateway's private-app unlock** (`require_authn` and public auth with
  a bearer key). gatewayd-internal reads the limit of an agent or
  `mcp-login` key with the key and keeps it in its key cache; a limited key
  opens only its projects' apps (403 `insufficient_scope`, audited as
  `instances.authn_scope`). An app-bound credential opens only its app; the
  gateway had dropped the binding.
- **Policy-controlled object uploads** on gatewayd-public refuse a limited
  key outside its projects (403). The edge store reads the limit
  (`APIKeyProjectLimit` moved to the shared query core).
- **Key rotation** copies the predecessor's limit to the successor in the
  rotation's transaction, on the account and organization paths, so
  rotating never turns a limited key into an account-wide one.

A gateway reads one primary-key row per agent key on a cache miss.

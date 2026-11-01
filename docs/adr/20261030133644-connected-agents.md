# ADR-20261030133644: Customers list and revoke the agents connected to their account

Date: 2026-10-30
Status: Accepted
Related: ADR-20261026195952 (hosted management MCP), ADR-20261028205920 (sliding connections), ADR-20261029085359 (agent keys), ADR-20261030065746 (client metadata documents)

## Context

An agent reaches a Gregale account in one of two ways. A browser sign-in
through the hosted MCP server (`api.<platform>/mcp`) mints an API key labelled
`mcp-login` that `mcp-bridged` holds for the connection. A headless agent uses
an API key carrying the `agent` scope. Customers could see neither as an agent:
the API keys page showed every hosted connection as an unnamed `mcp-login` key,
and only an operator could list connections, over the bridge's local admin
socket. A customer who lost a laptop or saw an agent misbehave had no way to
tell which key was which agent, and no way to cut every agent off at once.

The bridge also kept a connection whose key had been revoked until the
connection expired. It still counted against the 10 connections an account may
hold, so revoking a connection did not free a slot for a new one.

## Decision

### 1. A connected agent is an agent's API key

The account's connected agents are its usable API keys (status `active` or
`grace`, not expired) that are either `mcp-login` keys (kind `hosted_mcp`) or
carry the `agent` scope (kind `agent_key`). The key is the credential every
agent request is checked against, so revoking it is the revocation. A
self-hosted bridge using `--oauth-onboarding api-key` acts with the customer's
own key; that key is not an agent key and is managed on the API keys page.

### 2. The bridge names the client on its key

After a browser sign-in the bridge labels its new key once, with
`PUT /v1/agent-connections/self` authenticated by that key:

- `client_name`: the name the consent page showed (cleaned, at most 80
  characters);
- `client_site`: for a client identified by a metadata document, the host
  serving the document; for a registered client, the host of its HTTPS
  redirect URI (the authorization code can only be delivered there); empty
  for a loopback redirect.

apid accepts the label only from an `mcp-login` key, and the first write wins.
apid stores it in `api_key_agent_clients`, keyed by API key and deleted with it.
Labeling is best effort: the bridge never fails a sign-in on it, and an apid
from before this change answers 404.

### 3. API and CLI

- `GET /v1/agent-connections` lists the connected agents with kind, client
  name and site, key label and prefix, scopes, created, last used and expiry.
  It has the same gates as `GET /v1/keys` (read scope, MFA).
- `DELETE /v1/agent-connections/{id}` revokes one; a key that is not one of
  the account's connected agents is 404.
- `POST /v1/agent-connections/revoke_all` revokes every connected agent and
  returns the count.

Both revocations need an admin credential and MFA, like `DELETE /v1/keys/{id}`.
They go through the shared key revocation, so authentication caches are
invalidated and `key.revoked` is audited, with reason `agent_disconnected`.
Revoking all also audits `agent_connections.revoke_all` with the count. An
agent key cannot revoke other agents, because it has no admin scope.

`gregale mcp connection list` and `gregale mcp connection revoke <id> | --all`
call these endpoints. With `--socket`, the command keeps its operator meaning
(the bridge's local admin socket).

### 4. The bridge lets go of revoked connections and retires ended ones

Every MCP request already checks the connection's key with apid, so a revoked
agent fails on its next request. The bridge now also drops a connection as
soon as apid answers 401 for its key. Before refusing a sign-in at the
per-account limit, it re-checks that account's connections and drops those
whose keys apid rejects. The limit message names `gregale mcp connection
revoke`.

A connection can also end at the bridge: consent denied, the client revoking
its token, refresh-token reuse, an operator revoking the grant, or expiry.
Its `mcp-login` key used to stay valid in apid for up to a week, so the list
would show a denied sign-in as connected. The bridge now retires the key in
the background with `DELETE /v1/agent-connections/self`, authenticated by that
key; apid accepts it only from an `mcp-login` key, which can revoke nothing but
itself. A connection made with the customer's own key (api-key onboarding)
never retires that key.

## Consequences

- Customers can see which agent holds which access and cut one or all off
  without operator help; the dashboard can build on the same API.
- Revocation works even while the bridge is down: the key is refused at apid.
- Hosted connections made before this change are listed without a client name
  until the agent signs in again.

## Not included

- An organization-wide view and admin policy for members' agents (next step).
- The apps and environments picked at consent, which live only in the
  bridge's encrypted state.
- The address an agent was last used from.

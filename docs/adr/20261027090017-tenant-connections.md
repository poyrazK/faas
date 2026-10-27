# ADR-20261027090017 · Tenant connections: per-tenant OAuth for agents, held by the platform

- **Status:** proposed (owner decision D19, 2026-10-19).
- **Date:** 2026-10-19
- **Builds on:**
  - ADR-20261027090016 (capability gateway);
  - ADR-226 (platform tenants);
  - ADR-253 (customer-sealed outbound credentials), ADR-178 (fleet sealed-secret domain), ADR-249 (platform-held authorization).
- **Related:**
  - ADR-1190 (application Auth). That is end-user *sign-in*. This ADR is server-side *API authorization*, and the two share no tokens.
  - ADR-20261027090010 (agent runs), ADR-20261027090013 (sessions).
- **Decision:**
  - A **connection** is one platform tenant's OAuth grant to one provider: for example, tenant `acme-42`'s Google Drive, read-only.
  - The seller's end user creates it through a Gregale-hosted connect flow, using the **seller's own OAuth client**.
  - Gregale seals the tokens, refreshes them, and uses them only inside outboundd, when a run for that tenant calls a provider through the capability gateway (ADR-20261027090016).
  - Tokens never reach a VM, the seller's backend, logs or the model.
- **Why:**
  - **The hardest part of multi-tenant agents.** Sellers' agents act for many end users, each with their own Slack, Drive, GitHub or CRM. Building OAuth flows, token storage, refresh, revocation and per-user scoping is the most common piece of infrastructure sellers build around agents. Dedicated products exist to provide it.
  - **Gregale already has the parts:** platform tenants (ADR-226), sealed credential custody in outboundd (ADR-253), workload identity per run (ADR-250, ADR-20261027090016).
  - **Injection safety.** A token inside the VM can be exfiltrated by injected content. A token in outboundd can only be used through policy, budget and audit.
- **Consequences:**
  - New tables, a connect flow and callback hosts.
  - A token-refresh worker in outboundd.
  - Tool packs and authenticated passthrough for a first set of providers.
  - Connection webhooks.
- **Rejected alternatives:**
  - **Gregale-owned OAuth clients for every provider in the first release.** Provider app verification, scopes review and brand liability would sit with Gregale. Sellers' end users should see the seller's name on the consent screen. Gregale-owned clients can come later for long-tail providers.
  - **Returning tokens to the seller.** The seller would then store them, and the point of custody is lost.
  - **Putting tokens in the VM environment.** Rejected for the reasons above, and by ADR-249's own reasoning.
  - **Building on ADR-1190.** Sign-in tokens prove who the user is. They are not long-lived API grants with refresh.

## Context

The capability roadmap (D19) lists connections second, after the capability gateway. Agent sellers (D1) run one agent for many tenants. The run already knows its tenant (ADR-226, ADR-20261027090016 §2); what's missing is the tenant's authority to act in their own SaaS accounts.

## Decision

### 1. Objects

| Table (owner) | Holds |
|---|---|
| `oauth_clients` (apid) | The seller's OAuth client per provider: client ID, sealed client secret, allowed scopes, redirect host |
| `connections` (apid intent; outboundd token state) | Account, platform tenant, provider, granted scopes, status (`pending`, `active`, `expired`, `revoked`, `error`), and the sealed access and refresh tokens. A CHECK covers the status column |
| `connection_events` (outboundd) | Created, refreshed, failed, revoked: audited, with no token material |

- **Sealing.** Tokens and client secrets are sealed to the fleet recipient (ADR-178), as ADR-253 does for customer credentials. They are decrypted only in outboundd's memory.
- **Ownership.** apid writes intent: clients, starting a connection, revoking. Only outboundd writes token state: exchange, refresh, expiry.

### 2. The connect flow

1. The seller's backend calls `POST /v1/connections:start {tenant, provider, scopes, return_url}` with its API key. It receives a single-use connect URL, bound to account, tenant, provider and scopes and valid for 10 minutes.
2. The end user opens the URL. It redirects to the provider's consent screen with the seller's OAuth client, PKCE, and a `state` bound to the connect URL.
3. The provider redirects to Gregale's callback host, where outboundd exchanges the code and seals the tokens. The connection becomes `active`.
4. The end user is returned to `return_url`, and the seller receives a `connection.created` webhook carrying no tokens.

- **The callback host** is its own registrable domain, separate from preview URLs, so the provider redirect allowlist names only Gregale's callback host.
- **An embeddable connect component** (a React button and modal) comes after the API.

### 3. Refresh and lifecycle

- **Refresh.** outboundd refreshes access tokens before expiry, and on any 401 retries once after refreshing.
- **Failure.** A refresh failure sets `expired` and sends a `connection.expired` webhook, so the seller can ask the user to reconnect.
- **Revocation.** It comes from the seller (`DELETE /v1/connections/{id}`), from tenant deletion or suspension (ADR-226), or from the provider. It wipes the sealed tokens and calls the provider's revoke endpoint when one exists.

### 4. Use inside runs

- A run's capability policy (ADR-20261027090016 §3) lists the providers it may use. outboundd uses only the connection of the run's own tenant, and only with scopes within those granted.
- **Two ways to use a connection:**
  - **Tool packs:** MCP tools for the first providers, such as `github.create_pr_comment`, `slack.post_message`, `gdrive.search` and `gdrive.read`. Each tool is marked sensitive or not.
  - **Authenticated passthrough:** `provider.request {method, path, body}` to the provider's fixed API origin, with the `Authorization` header injected by outboundd. Fixed origin only; `CONNECT` and redirects are refused.
  - The provider's own MCP server, where one exists, called with the tenant's token, is preferred over writing a new tool pack.
- **Trust.** Connections for providers that carry user-written content (email, issue trackers, chat) are marked untrusted for taint (ADR-20261027090016 §4).

### 5. Providers in the first release

GitHub, GitLab, Slack, Google (Drive, Gmail read-only, Calendar), Microsoft 365 (OneDrive, Outlook read-only), Notion, Linear, Jira and HubSpot. Others follow on demand, through passthrough, before getting a tool pack.

### 6. Security

- **Where tokens live.** Tokens are never in a VM, a response body, logs, metrics or traces. Webhooks and API reads return metadata only.
- **Tenant isolation.** A run can use only its own tenant's connections. The tenant claim comes from vmmd's assertion (ADR-20261027090016 §2), never from the guest.
- **Least scope.** The connect URL fixes the scopes. The policy can only narrow them, never widen them.
- **Audit.** Every use is a capability audit record (ADR-20261027090016 §6), with the provider, tool or path, and the decision.
- **Provider terms.** Sellers are responsible for their OAuth client's compliance with each provider's terms and verification. Gregale documents this.

### 7. Pricing and limits

Connections are a software feature and are free. Limits exist to bound abuse, not to charge.

| Name | Hobby / Pro / Scale |
|---|---|
| `ConnectionsPerAccount` | 100 / 1,000 / 10,000 |
| `OAuthClientsPerAccount` | 5 / 25 / 100 |
| `ConnectionRequestsPerTenantPerMinute` | 60 / 300 / 1,200 |

## Metrics (§12 additions)

- `connections_active{provider}`
- `connection_refresh_total{provider,outcome}`
- `connection_requests_total{provider,decision}`
- `connection_start_total{outcome}`

## Rollout

All steps stay on local branches, behind the internal `agents` capability.

1. This ADR, after ADR-20261027090016.
2. Tables, sealing, and the apid intent routes.
3. The connect flow and callback, starting with GitHub and Slack.
4. outboundd refresh, passthrough, and the first tool packs.
5. Webhooks and the embeddable component.
6. The remaining first-release providers.

## Open questions

1. Gregale-owned OAuth clients for long-tail providers, and who carries their verification.
2. Whether connections may be shared across tenants of one seller (an org-wide Slack bot, for example) or stay strictly per tenant (proposed).
3. Token-encryption keys per account instead of the fleet recipient.

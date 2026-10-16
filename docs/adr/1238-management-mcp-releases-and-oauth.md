# ADR-1238: Reviewed MCP releases and a delegated OAuth bridge

- Status: Accepted
- Date: 2026-10-11
- Related: ADR-1237
- Amends: ADR-1237

## Context

ADR-1237 connects local agents to read-only Gregale management. Agents also need
bounded operational inspection, reviewed release actions, and remote access.
The customer API owns deployment intent, checked rollback, idempotency, and
transactional serving-release guards. Its existing OAuth metadata describes
OIDC token exchange, not a general authorization-code server.

## Decision

Extend `pkg/mcpmanagement` with app-scoped history, instance projection, health,
route coverage, operational summaries, and rollback status. Use typed customer
API methods throughout; import no database or daemon implementation.

Release previews capture immutable source, account, API/profile, app identity
and configuration, default scope, latest deployment, and serving release. Keep
opaque review handles for 15 minutes in bounded connection memory. Writes are
disabled by default and write tools are absent from that catalog. Applying an
image plan stages at zero traffic after preflight revision checks; explicit
promotion calls the API's atomic expected-serving guard. Checked rollback pins
its target and expected current release. Mutations reuse plan-specific idempotency
keys; ambiguous retries retain the original payload and key. Staging preflight is
not an atomic deployment-insert guard, so it never cuts production traffic over.
Service/worker releases and source uploads retain specialized CLI workflows.

Add Streamable HTTP with a separate OAuth bridge on a configured public HTTPS
`/mcp` resource. Run a single process behind approved TLS ingress, defaulting to
a loopback listener. This adds no platform daemon, public control-plane listener,
schema migration, or API authorization issuer. Publish protected-resource and
authorization-server metadata. Clients register callbacks from an operator
allowlist and use authorization-code S256 PKCE bound to resource and redirect.
Loopback entries may omit a port for ephemeral desktop callbacks; exchange still
binds the exact registered redirect.

Each browser flow verifies a user-supplied dedicated API key, shows its resolved
account, and requires explicit consent. Writes require both bridge configuration
and write-scope consent. Issue opaque, resource-bound read/write MCP tokens.
Never accept a host CLI login or API key as an MCP bearer. Each grant gets its
own SDK server, API client, and plans. Revalidate account identity on every remote
request. API scopes, MFA, entitlement, integrity, binding, and rollout gates remain
authoritative.

Enforce HTTPS resource configuration, Host/Origin checks, Secure HttpOnly SameSite
consent cookies, CSRF, callback allowlists, bounded protocol/state sizes, expiring
single-use codes, refresh rotation/replay revocation, and explicit revocation.
Store token/code lookup hashes and account credentials only in process memory.
Access tokens last 15 minutes and grants eight hours; restart disconnects clients.
No credentials enter logs, tool results, persisted configuration, or command args.

## Consequences

Local agents retain profile behavior. Remote clients use individual credentials
and cannot borrow another grant's release handle, even in the same account. Read
grants cannot discover or execute write tools. The bridge does not assume that
apid provides an authorization-code issuer.

The bridge needs HTTPS ingress, callback configuration, and ingress rate limiting.
It is single-process with API-key browser onboarding. Shared durable grants,
native browser sign-in/consent, full source planning, and service rollout adapters
can follow without weakening this boundary. Local MCP support does not deploy
the remote bridge automatically.

## Validation

Official SDK clients exercise stdio and real HTTPS Streamable HTTP under both
supported protocol versions. Tests cover projected inspection, stale identities,
configuration and releases, zero-traffic staging, ambiguous idempotent retries,
traffic guard conflicts, checked rollback progress, bounded waits, and cancellation.
OAuth tests cover account review, PKCE/resource/client/redirect binding, consent
forgery, explicit write consent, account/plan isolation, credential revocation,
refresh rotation/replay revocation, metadata, configuration, and callback allowlists.

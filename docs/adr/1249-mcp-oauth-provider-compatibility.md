# ADR-1249: MCP observer OAuth provider compatibility

## Context

The guided flow implements PKCE and sealed grants but generic parameters do not
request offline consent on all providers. Some registrations require a fixed
loopback port, token authentication in the POST body or RFC 8707 resource binding.
Provider metadata can diagnose declared incompatibility before browser consent.

## Decision

Configuration supports explicit generic, Google and OIDC profiles without
rewriting endpoint URLs. Google adds access_type=offline and prompt=consent,
defaults confidential clients to client_secret_post and recognizes the canonical
Google email/profile scope URI aliases. OIDC requests openid and offline_access
plus consent. Generic preserves existing behavior. Effective scopes remain within
the shared 16-scope bound. Confidential clients can explicitly select basic or
post; public clients select none. Credential presence must match the method and
missing credentials never fall back to public authentication.

A reviewed HTTPS resource identifier is sent unchanged in authorization, code
exchange and refresh grants. It is an identifier, not a network target. Customers
must configure a resource their provider recognizes and their MCP app validates.
The observer continues to probe the app's canonical gateway origin. Providers
without resource support can leave the field empty.

A configured callback port pins the exact IPv4 loopback redirect port. The CLI
uses that port, fails before authorization when occupied and cannot override it
with another port. Dynamic configurations can choose an explicit CLI port for
one attempt; the sealed flow still binds the exact redirect URI.

Optional issuer and metadata URLs are explicit reviewed HTTPS configuration.
Metadata fetches reuse guarded egress, redirect refusal, body bounds and request
deadlines; no bearer or client credential is sent. Checks compare issuer and both
endpoints exactly, S256 support, token authentication, response/grant types and
requested scopes. Absent token authentication metadata has the RFC 8414 basic
default. Missing PKCE/grant/response declarations and unadvertised scopes are
unverified; scope lists need not enumerate all valid provider API scopes.

The existing authenticated connection API adds a read-only check action, and the
CLI exposes provider-check. Begin blocks endpoint mismatches, explicitly declared
incompatibilities and unavailable configured metadata without minting a flow.
Incomplete metadata permits the actual PKCE flow but cannot establish verified
compatibility. Checks return fixed categorical reasons, never provider bodies or
source errors. Discovery never changes a reviewed endpoint or chooses a profile.

## Consequences

Provider-specific consent and client authentication are explicit and consistent
across connection and renewal. Compatibility changes participate in existing
configuration revision fencing and discard a prior connection when its binding
changes. An append-only JSON constraint protects stored intent; rollback refuses
while compatibility fields remain configured.

Metadata proves declarations only. Live provider issuance, refresh, revocation,
resource enforcement and MCP authorization require separate acceptance evidence.
Profiles do not introduce dynamic registration, arbitrary authorization parameter
passthrough, private provider egress or automatic endpoint discovery.

# ADR-1242: Human browser approval for management MCP promotions

## Status

Accepted. Extends ADR-1240 and ADR-1241.

## Context

Protected environment promotion required a human to copy an API approval token
into an agent tool call. Agents need a review link and a way to resume without
receiving approval credentials or being allowed to approve themselves.

## Decision

On protected apply, start a five-minute device review through apid. A sealed
ticket binds the account, immutable project/source/target environment IDs, exact
promotion preview token, reason and expiry. Render review in the existing
session-authenticated `/cli-auth` surface, so sign-in, MFA, session revocation and
console forwarding remain authoritative. Show release changes and promotion
hash. Escape agent-provided text and require CSRF bound to this account and
request. Rebuild the promotion preview at review and exchange; reject identity,
configuration, qualification or release changes.

Reuse durable `cli_auth_codes` exclusively as an atomic human-claim gate, using
a domain-separated hash of a 256-bit private polling secret. The sealed browser
ticket contains its hash, never the secret. The normal CLI code parser cannot
address this domain, and the review never mints a login key. The public review
link alone cannot exchange approval. Both request and exchange require an
authorized deployment credential and account ownership.

After human claim, apid derives the private approval credential using a keyed
domain-separated MAC. The existing unique approval-token hash ensures repeated
or concurrent exchange returns the same unconsumed approval, with the original
expiry. A consumed or expired approval cannot be recreated. This uses existing
durable primitives without a new table. MemStore enforces the same unique hash
constraint as Postgres.

Review minting bypasses the plaintext idempotency-response cache. A lost mint
response can leave an unused request that expires after five minutes; it cannot
change releases. Approval exchange is retryable through the deterministic
credential and durable unique hash, without storing plaintext secrets in apid.

The MCP bridge returns only `approval_pending`, the review link, expiry, plan ID
and retry interval. It persists private polling material and the resulting
approval in the encrypted plan journal. Retry apply polls once, then resumes
using the original guarded promotion and idempotency key. No human-approval tool
is exposed. Existing manually approved token workflows remain supported.

Run real Codex remote acceptance against an isolated TLS/OAuth bridge with native
browser onboarding, DCR, PKCE, discovery/read calls, durable reconnect, refresh
and revocation. Advance expiration only inside fixture state and a disposable
client profile. Certificate verification stays enabled. CI publishes only public
result reports; private diagnostics and credentials remain local. Allow Codex's
random loopback callback suffix only through an explicit operator-configured
`http://127.0.0.1/callback/*` rule. The wildcard accepts one bounded unreserved
component, and registered callbacks remain exactly bound throughout OAuth.

## Consequences

Closing the review page leaves it pending until its five-minute expiry. Changed
or expired reviews need a new plan. A stable session sealing key is required to
resume the native review across apid restart, alongside bridge encrypted state.
The existing deployment API remains the sole executor of customer intent.
Live-account acceptance and other agent clients remain separate validation.

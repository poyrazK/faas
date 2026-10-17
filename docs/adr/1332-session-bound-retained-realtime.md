# ADR-1332: Bind retained realtime authorization to the verified login session

- **Status:** accepted for implementation; deployed qualification pending
- **Date:** 2026-10-15
- **Amends:** ADR-1190 and ADR-1385
- **Decision:** Carry a verified session identity into retained WebSocket and SSE
  authorization callbacks, and refresh retained WebSocket JWT verification.
- **Why:** A callback that checks whether a user has any live session allows a
  logged-out device to borrow another device's session.

## Identity handoff

The production JWT authorizer returns the verified subject, optional `session_id`
and expiry through `JWTIdentityAuthorizer`. The shared JWKS verifier explicitly
extracts the session claim and requires `exp` for retained transports. These
transports reject expired identities even inside the verifier's clock-skew
window. Legacy WebSocket admission retains its existing contract.

Each retained connection pins its original subject, session and expiry. Client
frames cannot supply or replace that identity. Channel callbacks receive the
session ID alongside the principal; access tokens are never put in callback
payloads, connection snapshots or durable callback events. A custom principal
projection must still match after refresh.

One connection-level positive JWT decision lasts at most the existing 30-second
channel grant lifetime. Commands, the writer, channel polling and an idle timer
share its refresh. Endpoint changes invalidate it. The expiry timer closes idle
sockets and output guards also check expiry. Invalid or expired identities end
the connection; verification outages close it for retry. Key removal follows the
existing JWKS cache freshness and outage-grace contract, not an immediate key
fetch on every refresh. Reconnect with a refreshed token; no in-place identity
replacement protocol is added.

Existing embedders implementing only `JWTAuthorizer` can still authorize custom
callbacks using their principal. They provide no session ID or expiry handoff
and cannot grant session-bound Application Auth channels. The production daemon
implements the richer interface for both retained WebSocket and SSE admission.

## Auth callback and rollout

The Auth template requires UUID subject and session IDs from its authenticated
realtime callback. It uses the existing transaction-local `request.jwt.claims`
helper and `gregale_auth.authorize`, so the named session must belong to that
user and be active; membership, policy, bans and deletion checks are preserved.
There is no fallback to `authorize_subject` and no SQL migration is required.

Deploy the daemon before redeploying Auth apps from the updated template, then
reconnect retained clients. An updated Auth app rejects callbacks from older
daemons that omit the session. Until the Auth app is updated, its older callback
still has the subject-level contract. Logout is enforced at the next channel
grant refresh, including ADR-1385's idle cleanup delays. Inbox/direct-message
session authorization and subscription-ticket issuance/revocation keep their
existing contracts; JWT validity now guards all output on retained JWT sockets.
Existing preview and provider/native qualification gates remain in force.

## Validation

Cover signed claim handoff, required/expired expiry, signature rejection, policy
changes, concurrent refresh, slow verification, buffered output, idle expiry,
SSE, browser/header credentials and a forged client session field. With two
sessions for one user, removing one must revoke only its channel subscriptions.
Run the realtime and realtimed packages, race checks and the Auth template suite.
Deployed cross-node and live Auth database qualification remain separate gates.

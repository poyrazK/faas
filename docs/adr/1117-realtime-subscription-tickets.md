# ADR-1117: Short-lived channel subscription tickets

Status: operator preview

## Context

Apps with their own login system need a scoped realtime credential without an
OIDC provider or distributing backend platform credentials to clients.

## Decision

Provide an app-scoped, MFA-protected backend issuance API for an explicit principal
and exact channel list. Credentials contain 256 random bits, use the `grt_` prefix,
and are stored only as SHA-256 hashes. PostgreSQL controls issued/expiry timestamps.
TTL is 1-300 seconds; 1-32 unique channels and 1115 unexpired tickets per endpoint
are operator-preview bounds recorded in pkg/api/limits.go. Issuance locks the
endpoint, removes its expired records, then atomically checks capacity and inserts.
No signing key distribution, JWT issuer or new daemon is needed.

Nodes verify through the existing bounded private apid RPC, restricted to active
nodes and enabled endpoints. Ticket reads always enforce the durable principal
session cutoff and channel denies, independent of OIDC mode. Nodes revalidate
active WebSocket tickets at most once per second and check expiry before writes;
SSE revalidates before events and at polling boundaries. Lookup failures fail
closed. Exact granted channels do not imply parent/child access; parent denies
still apply to private activity scopes. Expiry terminates the connection; there
is no in-place refresh. Session/channel revoke APIs require access preview and
accept non-OIDC endpoints when ticket preview is enabled.

WebSocket tickets require v2. Browser tickets use the existing credential-bearing
subprotocol with explicit allowed origins, stripped before protocol negotiation.
SSE uses the Authorization header. Query-string credentials are unsupported.
A dedicated retained subscription branch avoids presence leases and ephemeral
fanout; only subscribe, unsubscribe and non-durable ack frames are accepted.
Inboxes, signal/presence writes, read-progress mutations, named durable checkpoints
and direct messages are excluded. Connect/disconnect callbacks and per-channel
application authorization callbacks are bypassed for ticket connections: the
backend issuance decision is the authorization authority. Existing connection
limits, subscription limits, history bounds, queue limits and replay rules apply.

## Consequences

Enable ticket preview on apid and every realtimed node, with resume preview and
private history/ticket RPC configured. The feature adds an opt-in credential path
without changing endpoint legacy authentication; use an endpoint whose existing
legacy path is appropriately restricted. The backend must authenticate the user
and authorize every requested channel before issuing; never expose issuance with
an arbitrary user-supplied principal/channel list. Tokens must not enter logs,
audit metadata, URLs or persistent browser storage. A ticket is a reusable bearer
credential until expiry, not a one-time connection nonce. Replicated DB reads add
load; no offline verification or availability fallback is provided.

Ticket subscribers poll retained history and do not advertise fleet ephemeral
routes or presence. Browser factories obtain a fresh ticket on reconnect; SSE
closes normally at ticket expiry so the existing helper can reconnect. Runtime
qualification, migration application and production activation are deferred.

# Browser sessions with scoped grants

This preview delegates a small part of an application's durable-session API to
an end user. A trusted application backend must authenticate its user and check
access to the document/customer before minting a grant. Gregale verifies the
capability; it does not infer application user identity or document permissions.

## Enablement

Keep production enablement disabled until the separate private GCS/KVM acceptance
passes. `FAAS_DURABLE_ENTITY_BROWSER_SESSIONS_ENABLED=1` additionally requires
`FAAS_DURABLE_ENTITY_SOCKETS_ENABLED=1`, durable invocation preview, app allowlisting,
WebSocket-enabled app, and a plan admitting both invocation and WebSockets.

Grants use the configured private durable bucket and conditional writes under
`session-grants/v1/`. No SQL application-state database or signing-key service is
required. Existing account/API-key authentication still uses Gregale's control
plane. Records contain only a SHA-256 credential digest and issuer key ID, never
plaintext grant credentials or owner key hashes. Provision a bucket lifecycle
rule **restricted to this prefix** to remove expired records after at least one
day; do not apply that rule to entity snapshots/manifests. Grant records are not
included in entity retained-byte accounting, export, or restore. TTL limits
credential validity, not automatic object deletion.

## Backend contract

Owner API key with the existing deploy/write scope:

```http
POST /v1/apps/my-app/entities/session-grants
Authorization: Bearer <server-side owner key>
Content-Type: application/json

{"namespace":"recoverable-rooms","key":"document:123","session_id":"11111111-1111-4111-8111-111111111111","generation":0,"origin":"https://app.example.com","commands":["open","message","close","status"],"ttl_seconds":60}
```

Optional `env` and `platform_tenant_id` use normal entity admission. The grant
binds immutable account/app/environment/tenant/entity identity plus session UUID,
generation, exact browser Origin, explicit command allowlist and expiry (1–300
seconds). Ordinary persisted account API keys can issue grants; session cookies,
synthetic tenant bearers and per-app deploy-token principals cannot mint them.
Generation is chosen by the trusted backend; the recoverable guest helper checks
that it is current. Register that helper for the selected namespace.

Response (201, `Cache-Control: no-store`): `grant_id`, `credential`, `expires_at`,
`connect_path`, `protocol`. Return only these scoped credentials and the bound
session/generation to the authorized browser. Keep the owner key on your server.

`DELETE /v1/apps/my-app/entities/session-grants/{grant_id}` revokes a grant using
conditional writes, with account/app checks. Revocation is idempotent while the
record exists. Expiry and revocation are checked before each frame, including
before receipt replay. An invocation already admitted may finish and acknowledge.
Bucket errors fail closed. Issuer deletion, revocation, expiry, scope changes,
app disablement, and normal tenant/environment admission are rechecked.

## Browser contract

Import the standalone browser-safe subpath:

```ts
import {connectDurableEntityBrowserSession} from '@gregale/sdk-node/durable-entities/browser';
const socket = connectDurableEntityBrowserSession('https://api.example.com', grant);
```

The connection uses `gregale.entity.v1` and `gregale.grant.<credential>` WebSocket
subprotocol offers. The server selects only the public protocol. Credentials
never enter query strings. Configure request logging to redact
`Sec-WebSocket-Protocol`, as with other authentication headers. Exact Origin is
required; HTTPS origins are accepted, plus HTTP loopback for development. No
wildcards, `null`, cookie authentication, Authorization header or URL selectors
are accepted on the browser connection path.

Send the existing generation-qualified lifecycle frames. `open`, `message`,
`close` and `status` are individually grantable. Commands for another session,
generation, request identity or undeclared command are rejected before dispatch.
`advance` and `reclaim` remain service-only. Status returns the session's current
snapshot; it does not authorize a mutation in another generation.

The browser transport shares service-socket connection limits and ordered,
one-frame-at-a-time committed acknowledgements. Persist the exact pending frame
for replay after lost acknowledgement; obtain a fresh scoped grant to reconnect.
Do not persist grant credentials with the outbox. The example
`examples/durable-entity-browser/client.ts` shows obtaining a grant from your
application backend and opening a session. It requires your backend's user
authentication/authorization implementation and a browser bundler.

This does not provide broadcast, presence, automatic reconnect, transparent
hibernation, or authentication for arbitrary HTTP entity invocation. Native
multi-node revocation, GCS failure, browser Origin, takeover, and restart acceptance
remain part of the independent host/bucket acceptance gate.

## Source qualification and native handoff

[CI run 38130306595](https://github.com/poyrazK/faas/actions/runs/38130306595)
passed for source `c62def181211bf05cb817695d383cccea13fcf41`.
The Node build, 16 focused SDK tests, packed starter acceptance, four Go
race-tested durable packages, focused CLI checks and nine authenticated
socket/API tests passed. Browser coverage includes safe URL/protocol construction;
API tests cover actual WebSocket upgrades, session/generation/control-command
fencing, Origin rejection, grant revocation, issuer deletion, expiry and private
record credential hashing. This is source/HTTP-fixture evidence, not native
GCS/KVM or real-browser acceptance. No local tests/builds were run.

The independent acceptance host should verify HTTPS/WSS from a real browser,
reconnect with a renewed grant and exact saved frame, and simultaneous sockets
on two nodes followed by grant/issuer revocation. It should also check tenant
suspension and environment recreation, provider read failures, uncertain mint
and revocation writes, header redaction, prefix-restricted cleanup, and the
existing takeover/restart scenarios. Keep the production preview flags off until
that evidence is available. Authenticate users and authorize entity access in
the application backend before minting grants.

For shared admission limits and atomic grant-byte accounting, see
[tenant quotas](durable-entity-tenant-quotas.md). Quota mode moves new grants into
`tenant-quotas/v1/` and uses opaque scoped grant IDs/credentials. Its records must
not receive the standalone-grant TTL cleanup rule. Existing standalone grants
need renewal after a coordinated quota-mode rollout.

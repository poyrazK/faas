# ADR-1113 — Managed realtime access revocation

- **Status:** proposed, operator-gated preview
- **Date:** 2026-10-10
- **Decision:** Persist per-endpoint OIDC principal channel denies and a monotonic session cutoff. Realtime nodes read these controls through the private fleet RPC and enforce them at admission, subscription, inbound handling and outbound delivery.

## Why

Existing application callbacks authorize subscriptions at admission. Membership removal otherwise requires the application to locate and disconnect every matching socket, including sockets on nodes that were temporarily unreachable. Durable access controls let the application express one revocation and let the managed data plane enforce it.

## Contract

Authenticated, MFA-protected app-scoped management reads/writes require the normal read/write scopes. `revoke_channel` adds a durable deny, also covering the channel's canonical private activity scopes. Nodes remove matching subscriptions and drop queued channel frames after refreshing controls. Other channel subscriptions and direct inbox access remain available. `restore_channel` removes only that deny; application channel authorization still applies and clients must subscribe again. It never restores a revoked session.

`revoke_sessions` advances a server-time cutoff without permitting rollback. Tokens must have a signed positive integer `iat`, no later than the node clock, and strictly later than the cutoff. Existing older sessions close, including live-only OIDC sockets. A fresh token can reconnect; banning a principal permanently remains an application authentication decision. Static bearer and unauthenticated endpoints have no per-user session controls.

Writes lock the endpoint so concurrent changes serialize and enforce preview bounds of 1115 principal records per endpoint and 64 denied channels per principal. Values are resource guards in `pkg/api/limits.go`, not new plan entitlements. Principal records, including session cutoffs, remain until endpoint deletion so cleanup cannot silently reopen an old token. The in-memory store mirrors mutation and deletion behavior.

Access reads use the existing private RealtimeFleet inbox-operation transport with an active registered node and enabled endpoint check. Realtime nodes do not connect to PostgreSQL. Admission reads controls before upgrade, then checks again before running the connection. Connections refresh at a one-second cadence with a two-second RPC timeout; expired snapshots must refresh before data writes. Failed refresh closes the connection. Recovering nodes fetch durable controls before delivery. The API confirms persistence rather than instantaneous fleet convergence, and an already in-flight WebSocket frame cannot be recalled.

## Rollout and qualification

Apply the migration first. Enable `FAAS_REALTIME_ACCESS_PREVIEW_ENABLED=1` on every realtime node before enabling it on apid; all nodes need the private history/fleet client, including nodes serving live-only OIDC connections. Do not disable enforcement while persisted revocations must remain effective. Mixed-version nodes lacking the read action fail closed when their preview is enabled.

Before promotion, qualify revoke/restore across nodes, unreachable-node recovery, concurrent subscription/revocation, queued frame suppression, scoped activity, old and freshly minted JWTs, clock synchronization, source-independent node lookup, and PostgreSQL/control-plane failure. Check request capacity at the intended connection count: the first implementation refreshes once per active OIDC connection per second. No fleet convergence receipt or production activation is included.

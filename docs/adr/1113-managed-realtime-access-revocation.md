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

## Amendment (2026-11-02): a change feed instead of a read per connection per second

Every connection re-read its principal's access every second through apid,
which costs three control-plane queries per read (endpoint check, node lookup,
access row), idle or not, so 5,000 connections were about 15,000 queries a
second. A failed read closed the connection with 1008, so an apid restart
closed every access-checked socket and told clients their token was bad.

- **Change feed.** `ChangeManagedRealtimeAccess` and both ticket revocations
  send `pg_notify('managed_realtime_access_changed', {endpoint_id, principal})`
  in their transaction. apid listens (one connection) and keeps the last 4,096
  changes in memory with a sequence and an epoch (`receivers.RealtimeAccessFeed`).
  Realtime nodes poll it once a second with the `access_changes` inbox action;
  apid answers from memory after one node lookup. A new epoch (listener
  (re)connected or fell behind) or a node further behind than the feed holds
  answers `reset`.
- **Node cache.** A node caches access per (endpoint, principal) and ticket
  claims per (endpoint, ticket hash), shared by its connections, with one read
  in flight per key. An entry lasts until the feed names its principal, at most
  a minute; a reset marks every entry stale. While the feed is unavailable
  (older apid, listener down) an entry lasts one second, the old cadence.
  Connections still check every second, against the cache. A revocation now
  reaches sockets within about two seconds (feed poll plus check), not one.
- **Failures.** A failed read serves the last answer for up to two minutes and
  is retried at most every five seconds; after that the connection closes with
  1013, not 1008. A revocation cannot be written while apid or its database is
  down, so the window is a change committed in the last poll before an outage.
  A principal the feed named is dropped from the cache and gets no last answer.
  This replaces "Failed refresh closes the connection".
- The feed is per apid process; with several apids behind one address the
  epochs alternate and nodes fall back to per-second reads, correct but no
  cheaper.

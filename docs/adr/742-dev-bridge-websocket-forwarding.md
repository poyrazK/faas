# ADR-742 · WebSocket forwarding through Dev Bridge

- **Status:** accepted for the internal, operator-gated Dev Bridge capability
- **Date:** 2026-10-08
- **Decision:** Extend the ADR-378 session scope to carry WebSocket upgrades
  in both bridge directions: scoped remote traffic to the local process, and
  the local process to an allowed remote dependency. Each upgraded connection
  is admitted against a per-session budget, bounded by idle and byte limits,
  rechecked against the session's authority, and closed on revocation, expiry
  or laptop reconnect. Other protocol switches, gRPC, raw TCP and database
  tunnels remain out of scope.
- **Why:** Frontends that rely on live updates (dashboards, chat,
  collaborative editing, framework dev servers) could not use the bridge at all.
  ADR-378 deliberately listed WebSocket forwarding as a future contract, not
  an implicit widening of session scope.

## Context

The laptop holds one HTTP/2 tunnel to `bridged` over an outbound WebSocket.
HTTP/2 forbids `Connection` and `Upgrade`, so a protocol switch cannot pass
through the tunnel as a normal request. The local side proxies to a fixed
loopback origin and must never let a remote request choose a destination.

## Decision

- **Scoped traffic, remote to laptop:**
  1. `bridged` accepts only `GET` requests with `Upgrade: websocket`, after
     the same `AuthorizeRequest` check as HTTP. Any other upgrade gets 501
     `dev_bridge_upgrade_unsupported`.
  2. It opens one tunnel stream marked `X-Gregale-Dev-Bridge-Upgrade: websocket`.
     The `X-Gregale-Dev-Bridge-` prefix is stripped from all inbound traffic
     before it enters the tunnel, so only the relay can set the marker.
  3. The CLI replays the handshake against the same fixed loopback host,
     after removing hop-by-hop headers and bridge credentials. It answers
     `200` plus the marker only when the local process returns `101`.
     `bridged` then hijacks the caller's connection and writes the `101`
     itself.
  4. A declined handshake is relayed as an ordinary `no-store` response.
- **Dependencies, laptop to remote:** the existing dependency route performs
  the switch through `httputil.ReverseProxy` after `AuthorizeDependency`
  (and context-route) checks. The hijacked caller connection is wrapped so
  the same limits apply.
- **Limits** (`pkg/api/limits.go`), with one budget shared by both directions:
  - `DevBridgeMaxUpgradedConnections = 8` per session; 429
    `dev_bridge_upgrade_limit` beyond it.
  - `DevBridgeUpgradeIdleTimeout = 5m`.
  - `DevBridgeUpgradeMaxBytes = 64 MiB` per connection per direction.
  - Upgrades use their own tunnel streams. The laptop accepts
    requests + upgrades concurrent streams, so a held socket never starves
    ordinary requests.
- **Revocation and fencing:**
  - Every upgraded connection repeats its authorization check every second
    against the current session row.
  - `CloseSession` closes all of the session's upgraded sockets.
  - A scoped-traffic tunnel ends when the laptop connection that carried it
    ends, so a reconnect never inherits an older socket. Dependency sockets
    do not use the tunnel; they end on revocation, expiry or their own limits.
- **Off by default:** a relay without `WithUpgradeLimits` refuses every
  upgrade. An older `bridged` configuration cannot silently widen what
  crosses the bridge.
- **Inspection:** the inspector lists each upgraded connection with its
  redacted path, status, duration and byte counts per direction. Frames,
  query strings, headers and bodies are never recorded.

## Consequences

- WebSocket frontends and dependencies work in a bridge session with the same
  authority, production-caller denial and cache/mirror bypass as HTTP.
- A long-lived socket consumes a session slot until it closes, idles out or
  hits its byte cap. Applications must reconnect after those closures, as
  they already must after a bridge reconnect.
- Native split-box acceptance for upgrades joins the pending ADR-378 native
  run before any rollout.

## Rejected alternatives

- **Generic protocol switch (any `Upgrade`).** Arbitrary upgraded protocols are
  effectively raw TCP, which ADR-378 keeps out of scope.
- **Count upgrades against the request concurrency limit.** A few idle sockets
  would block all ordinary requests to the local process.
- **Carry WebSocket frames as individual tunnel requests.** Adds framing
  semantics to the relay and breaks backpressure. A byte stream per connection
  keeps the relay protocol-neutral after the handshake.

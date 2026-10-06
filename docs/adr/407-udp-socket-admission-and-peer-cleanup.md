# ADR-407: Public UDP socket admission and peer cleanup

## Status

Proposed; public rollout and native qualification remain pending.

## Context

UDP transport and admission primitives do not themselves own a public socket.
The socket boundary must prevent source-policy bypass, cross-peer replies,
unbounded pending admission and quota retention after cancellation.

## Decision

One Server owns one immutable app/account/listener identity and UDP socket.
Serve closes the supplied socket on every exit, including configuration errors;
a caller must bind a new socket to retry.
Validate its route and source-prefix policy before serving. Empty allowlists
admit no traffic. Reject truncated packets, then apply source CIDRs and shared
account inbound packet/byte credit before acquiring a peer slot or copying data.
Key peers by client address, acquire shared global/account slots before queue
allocation, and bound target admission to 30 seconds. Require the target to
belong to the listener app with live instance/node identities, and use the
listener's declared guest port for forwarding.

Retain originating client identity/context on replies. A single bounded
64-record writer applies shared outbound budgets and one-second write deadlines;
ignore canceled replies and stop dequeuing when listener cancellation is already
visible. Do not report a shutdown deadline failure as an operational write error.
Peer completion cancels its context, removes its address entry, releases its slot
and updates fixed-vocabulary metrics. Listener cancellation closes the socket
and joins its writer, cancellation watcher and all peer workers.

Pools, ledgers and metrics are injected and shared by the production supervisor;
per-server defaults support standalone use. No address, port, app/account ID or
arbitrary error string becomes a metrics label. Metrics are nil-safe.

## Consequences

Real loopback socket tests cover peer isolation, binary/empty datagrams,
fail-closed source policy, invalid/canceled admission, resource-exhaustion outcomes,
shared account capacity across sockets, reuse after listener shutdown and cleanup.
They do not prove privileged namespace or guest VM behavior. Durable intent,
supervisor reconciliation, production wiring/firewall exposure, native load,
VM lifecycle and leak acceptance remain required before public rollout. Storage
remains stateless and Linux/amd64 remains the target.

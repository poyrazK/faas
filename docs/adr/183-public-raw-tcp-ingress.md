# ADR-183 · Public raw TCP ingress

- **Status:** proposed
- **Date:** 2026-09-18
- **Decision:** Add a dedicated Layer-4 ingress path for explicitly declared
  TCP listeners. The edge accepts a TCP socket, resolves a stable app-owned
  endpoint, and forwards bytes over vmmd's bidirectional
  `ForwardTCPStream` RPC to the selected guest port.

## Context

Gregale already supports outbound TCP from workers and has HTTP/TCP-shaped
public forwarding for normal requests and HTTP Upgrade protocols. Those paths
are not a raw protocol tunnel: they parse HTTP framing and cannot carry
PostgreSQL, MySQL, SSH, SMTP, or MQTT bytes faithfully. ADR-176 supplies the
manifest-level listener declaration, while ADR-177 supplies a node-local host
port lease for future direct binding; neither one by itself exposes a public
socket.

## Invariants

- Only a TCP listener declared in the app manifest may be selected. UDP stays
  guest-only until a separate datagram design exists.
- The public identity belongs to the app/listener, not to a parked instance or
  compute node. Per-instance host leases are therefore an implementation
  detail and must not become the customer-facing endpoint.
- The transport is protocol-neutral: no HTTP request head, header rewriting,
  or application parsing. Client half-close maps to gRPC half-close and then
  to the guest socket's `CloseWrite`.
- TLS is passthrough. Gregale does not terminate SSH, database TLS, or other
  application protocols at the edge.
- Each direction has a byte cap, connection/plan concurrency quota, idle
  timeout, and audit/metrics identity. A parked or stopped instance must be
  woken/admitted before the stream is opened; lifecycle teardown cancels the
  stream.

## Rollout

1. Ship the vmmd TCP bridge helper and `ForwardTCPStream` transport (this
   change).
2. Add a durable app-listener endpoint and a dedicated `tcpd` listener/router
   that resolves it, wakes the app through schedd, and uses the transport.
3. Add systemd/firewall exposure, quotas/metrics, migration/drain handling,
   and operator-facing API/CLI configuration.

The existing HTTP gateway, WebSocket/raw-upgrade bridge, and host-port lease
behavior remain unchanged during the rollout.

## Rejected alternatives

- Reusing `ForwardRawStream`: it requires an HTTP response head and is tied to
  Upgrade semantics.
- Multiplexing raw TCP onto the shared HTTP listener: arbitrary protocols do
  not provide an HTTP Host header, and a single port cannot safely infer the
  target without protocol-specific inspection.
- Exposing a VM or compute-node address directly: that leaks placement and
  breaks on park, restore, migration, and node replacement.

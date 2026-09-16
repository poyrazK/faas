# ADR-177 · Durable container host-port leasing

- **Status:** accepted
- **Date:** 2026-09-12
- **Decision:** Persist one lease per `(compute_node, instance, listener,
  protocol)` in `container_host_port_leases`, allocating the first available
  port in `30000–39999` per node and protocol.

## Context

Declared workload listeners already have a stable guest port and protocol, but
there was no node-local record for a future direct-ingress binder. Allocating a
host port in process memory would allow two scheduler or vmmd processes to pick
the same port and would lose the mapping across a restart.

## Decision

Gregale stores one lease per `(compute_node, instance, listener, protocol)` in
`container_host_port_leases`. TCP and UDP have independent port namespaces;
the first available port in `30000–39999` is selected per node. Acquisition is
idempotent for an existing listener and atomic for a batch of listeners. The
unique node/protocol/host-port constraint is the cross-process collision fence.

Schedd acquires leases for the declared `manifest.ports` listeners after
placement and before vmmd admission. It releases them when an instance enters
PARKED, STOPPED, or FAILED. The in-memory store uses the same deterministic
allocator for tests and local development.

The default public path remains the per-instance vmmd bridge. The lease is the
durable allocation primitive for direct host ingress; binding sockets, UDP
forwarding, and per-port TLS remain separate edge integrations. Keeping those
concerns separate preserves the existing tenant boundary and migration path.

## Consequences

- A two-node fleet can reuse the same host port on different nodes without a
  collision, while each node rejects duplicate TCP or UDP ownership.
- A failed boot cannot leak a port because the state transition releases the
  lease, and a scheduler restart can list the durable rows for reconciliation.
- The current ingress path does not change until a binder consumes the lease;
  this avoids coupling a public endpoint to a particular compute node before
  node-drain and migration support is ready.

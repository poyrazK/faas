# ADR-378 · Local processes in development environments

- **Status:** accepted for an internal, operator-gated HTTP capability
- **Date:** 2026-09-30
- **Decision:** Add a leased development routing overlay. apid owns session
  intent and authorization; an unprivileged bridge relay owns live laptop
  connections; gateways select the relay before VM selection for authenticated
  session traffic. Ordinary routing and Firecracker ownership remain intact.
- **Why:** Local breakpoints and local edit execution require a local process
  to participate in the remote application beyond remote source synchronization.

Sessions identify account, developer, project, environment, intercepted app and
explicit dependencies. Attachment and request credentials are distinct, random,
stored only as digests, and expire after one hour. Only owned, active services in
unprotected named development environments are eligible. Inspection never returns
credentials. Every routing boundary validates scope and current resource state.

The CLI opens an outbound WebSocket connection over TLS and forwards streaming
HTTP/2 requests to a fixed literal loopback HTTP process. It offers loopback
session/dependency proxies, bounded metadata inspection and reconnection within
the same lease. Connection replacement fences old cleanup by owner identity.
Disconnect fails requests without retrying against a deployed app. Transport
preserves backpressure and caller cancellation; concurrent streams are bounded.
HTTP/1 proxy hops explicitly permit duplex IO and flush response headers before
the upload finishes. Verified session requests bypass shared caches, edge answers
and mirror fan-out while retaining normal authentication and rate/budget limits.

An optional remote frontend is included in the allowed graph. Synchronous remote
service calls require explicit framework propagation of request authority. The
node-local service proxy verifies actual source-instance identity, normal service
bindings and the caller deployment's environment before applying session routing.
Trace identity is not authorization. Request authority is stripped from external
API calls; attachment authority never reaches workloads. Production callers and
release/revision overrides are rejected.

Selected webhook replay copies an account-owned provider-verified receipt to the
local target. It preserves the original receipt and delivery machinery, omits
expired provider proof, marks development replay, and records an independent
durable idempotency receipt before dispatch. Uncertain outcomes and process
crashes never trigger automatic redelivery. Receipt inspection survives revocation.
Metadata older than seven days after session expiry is pruned on new account
session admission, with receipt deletion cascading from the session.

`bridged` is a loopback-only, unprivileged optional control-plane unit with enforced
read-only database transactions. It requires explicit operator enablement and is
excluded from core fleet activation. apid and gateways fail closed when disabled.
There are no scheduler or VM lifecycle changes and no new public listener.

The initial deployment uses one relay owner, consistent with the current single
control-plane architecture. Multiple relay ownership, raw TCP/database tunnels,
WebSocket/gRPC forwarding, automatic asynchronous context propagation and dashboard
controls are future contracts. They do not silently extend this session's scope.

Local acceptance covers the remote frontend/local payments/remote inventory flow,
two developer isolation, ordinary routing, production caller denial, streaming,
cancellation, reconnect fencing, revocation, PostgreSQL store parity, selected
webhook deduplication and credential handling. Native split-box and edge lifetime
acceptance remain required for rollout; this ADR does not declare a public product
release. [The developer/operator guide](../dev-bridge.md) records the protocol,
limits, installation, rollback and verification boundaries.

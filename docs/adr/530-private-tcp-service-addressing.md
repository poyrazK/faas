# ADR-530 · Private TCP addressing between services

- **Status:** proposed
- **Date:** 2026-10-03
- **Amends:** ADR-169, ADR-170 (DNS answer), ADR-361 (internal traffic is not tenant egress)
- **Decision:** Give every app a stable, account-scoped service address in
  `198.19.0.0/16`. `<name>.svc.gregale` resolves to that address for an
  authorized same-account caller, and a guest connects to it on the target's
  natural port. The host translates the address to a node-local TCP service
  proxy in gatewayd-internal, which recovers the original destination,
  authorizes the caller, wakes the target and forwards bytes over vmmd's
  `ForwardTCPStream`.
- **Why:** The internal service mesh selects its target from the HTTP Host
  header. Raw TCP has no Host header, so PostgreSQL, Redis, MQTT and other
  non-HTTP protocols cannot be addressed between services. Customers have to
  expose such a component publicly or give up on it.
- **Consequences:** `redis://cache.svc.gregale:6379` works from any authorized
  workload in the account, on every compute node, while the target is parked.
  HTTP service calls are unchanged. Raw TCP calls carry no caller-assertion
  header and cannot carry method/path scopes.
- **Rejected alternatives:** listed under [Alternatives](#alternatives).

## Context

ADR-169 exposes the service proxy on the tenant-bridge address, and ADR-170
answers every `*.svc.gregale` name with that same address. The proxy learns
which service was meant from the Host header. That works for HTTP/1, H2C,
gRPC and Upgrade traffic (ADR-197). It cannot work for protocols that never
name their destination.

The building blocks for a byte-transparent hop already exist:

- `ForwardTCPStream` (ADR-183) carries half-closed, protocol-neutral bytes to
  a guest port on any compute node. vmmd counts an open stream as instance
  activity.
- The guest service proxy resolves the caller from the masqueraded source
  address (ADR-169).
- The name resolver handles preview and scenario scope. The authorizer
  enforces account, declared-binding, allowed-caller and preview policy.
- The waker coalesces internal restores with public ones (ADR-196).

The missing piece is a way for a connection to say *which* service it wants
without an application header.

## Decision

### Address model

`api.ServiceAddressCIDR` is `198.19.0.0/16`. It is a platform constant, not an
operator setting, because a stable address must not move when configuration
changes. The range sits inside the benchmarking block (RFC 2544). That block
is never a legitimate public destination, and the OCI puller already refuses
it (pkg/oci/egress.go).

Each app owns `apps.service_address_index` (1..65534). Its address is
`198.19.0.0 + index`. Indices are unique per account, not globally, so every
account has the whole block and allocation never exhausts across tenants.

Allocation happens inside the app-create transaction under a per-account
advisory lock. It chooses the lowest index that is not held by:

- a live app;
- an app deleted less than `api.ServiceAddressReuseQuarantine` (24 h) ago.

DNS answers live for 5 seconds, so the quarantine is far longer than any
cached answer. Preview churn still cannot exhaust an account over time.

### Data path

```
guest ──connect VIP:port──► netns forward: accept tcp to ServiceAddressCIDR
      ──masquerade (src = instance host IP)──► host nat prerouting:
          VIP tcp dport {10080, 10081[, 443]} → dnat to <bridge>      (HTTP mesh, unchanged)
          VIP tcp (any other port)            → dnat to <bridge>:10082
      ──► gatewayd-internal service TCP proxy
          SO_ORIGINAL_DST → (VIP, port);  source → caller instance
```

Ports 10080, 10081 and 443 on a service address always belong to the HTTP
mesh; the TCP proxy refuses them even if a target declares one. The host
forwards `:443` only on a node whose private HTTPS service listener is
staged. Today a guest reaches bridge `:443` only on such a node. An
unconditional DNAT would let a guest reach whatever else binds the host's
`:443`, for example the public edge on a single-box install.

The proxy steps, in order:

1. Map the caller's source address to its app and deployment. An unknown or
   ambiguous source fails closed.
2. Resolve `(caller account, VIP)` to a target app. An address from another
   account resolves to nothing.
3. Run the existing service authorizer. A caller that holds a target-owned
   method/path call scope is denied, because the scope cannot be enforced on
   raw bytes.
4. Accept the destination port only if it is one of the target's declared TCP
   `ports` (image `EXPOSE`, compose `expose:`, or the app API). Undeclared
   ports are refused.
5. Acquire a per-account session slot (`ServiceTCPSessionsPerAccount`).
6. Select a live replica: deployment traffic weights first, then the local
   node, then round-robin. If none is running, wake through the ADR-196 path
   (trigger `service.mesh`).
7. Forward with `gateway.TCPForwarder.ServeConnAwaitingDial`. It waits for
   vmmd to confirm the guest dial before reading from the client. If the
   dial fails, the replica is benched, the endpoint lease is dropped, and
   one alternate replica is tried. This covers the window after a park,
   when the 5 s endpoint lease still lists the parked replica.

A caller's project release graph pins TCP sessions exactly as it pins HTTP
calls. Features driven by HTTP headers do not apply to raw TCP: deployment
overrides, version affinity, the development bridge, scenario chaos, the
binding probe, and the caller-assertion header.

The idle timeout of an internal session equals the **target plan's idle
timeout**. vmmd keeps the target awake while a stream is open, so a pooled
but idle connection keeps a target alive no longer than an idle HTTP app
would. Billing is unchanged: plan RAM plus 8 MB per running second.

Internal TCP is a platform service, like the bridge proxy and DNS. Its netns
admission sits next to the existing service-proxy admission, ahead of the
ADR-361 egress port allowlist. Reaching a same-account service is not tenant
egress, and it does not consume `EgressExtraPortsMax`.

The guest network world is untouched (ADR-009). Service addresses are
destinations, never instance identities, and they are identical on every
node. An address cached inside a snapshot therefore stays valid after a
restore on another node. That is stronger than today's per-node bridge
answer.

### DNS

A `*.svc.gregale` A query is answered with the target's service address only
when all of these hold:

- the caller resolves from its source address;
- the target resolves through the same resolver the HTTP proxy uses, so
  preview and scenario scope still apply;
- both apps are in the same account and the target has an index;
- the caller's netns is service-address capable (see rollout).

Every other case, including any lookup error, keeps today's bridge-address
answer. Cross-account and unknown names stay indistinguishable. `.internal`
HTTPS aliases are unchanged. The answer is switched on by gatewayd-internal
`service_tcp_dns`, which requires `service_tcp_listen`.

The caller lookup (source address to live instance) is never cached. Host
addresses are recycled when instances end, so a cache keyed by source address
could hand a new instance from another account the previous caller's
answer. Only the name resolution is cached, per caller app, for the 5 s DNS
TTL.

### Customer surface

- **Reachable ports.** A service address exposes:
  - every TCP port in the app's `ports`;
  - each live deployment's serving port.

  Image `EXPOSE` lists are not persisted beyond the serving port they seed,
  so a multi-listener image must declare its extra ports.
- **Internal listeners.** `WorkloadPort.internal` marks a listener as
  internal-only. Compose `expose:` declares internal TCP listeners. Internal
  listeners are excluded from the public `--port-<name>` selector and from
  public raw TCP and UDP listeners. Without this flag, mapping `expose:` onto
  ordinary declared ports would have published them through ADR-176.
- **Reconcile ownership.** Reconcile owns only the internal subset of `ports`.
  - A service without `expose:` leaves the subset untouched.
  - API-declared public listeners are kept.
  - An `expose:` entry takes over a public declaration of the same TCP port.
- **Binding variable.** Each declared dependency adds
  `GREGALE_SERVICE_<NAME>_HOST=<name>.svc.gregale`.
- **One-time refresh.** The first project apply after release refreshes every
  caller's binding environment once, as ADR-384's port change did.

## Security boundary

- The proxy listens only on the tenant-bridge address and reserved port
  10082. Guests cannot reach that port directly, because no netns rule admits
  it. A connection whose original destination is outside the service block
  is rejected.
- The caller identity is the masqueraded host address. A guest cannot choose
  it.
- The service-address lookup is keyed by the caller's account. A probe of
  another account's address learns nothing.
- Non-TCP traffic to the block is dropped in the netns and on the host
  forward path.
- gatewayd-internal needs no new capability. The original destination comes
  from the root namespace's conntrack via `getsockopt`, not from
  `IP_TRANSPARENT`.

## Rollout and rollback

The pieces ship dark, in order:

1. Constants (this ADR).
2. Address allocation and backfill.
3. Netns admission and host prerouting behind `service_tcp_enabled` /
   `faas_service_tcp_enabled`.
4. The gatewayd-internal TCP proxy behind `service_tcp_listen`.
5. DNS answers.
6. Customer surface: compose `expose:`, `GREGALE_SERVICE_<NAME>_HOST`, CLI and
   docs.

When the flag is on, vmmd records `compute_nodes.service_address_ready_at` at
boot, before it prepares or wakes any VM. The stamp is sticky: later boots
with the flag on keep the earliest value, and a boot with the flag off
clears it. A failed write is not fatal. The node is then simply not
advertised, which is the safe direction. DNS answers a service address only
to a caller instance whose `started_at` is not earlier than that timestamp. Instances whose netns came
from an older vmmd keep the bridge answer. Enabling the feature therefore
needs no fleet recycle. The new netns field also makes ADR-149 prepared
namespaces from before the flag incompatible, so they are never reused.

Never run a vmmd build without this feature on a node while its stamp is
set. Such a build creates namespaces without the admission but does not
clear the stamp. To roll back, turn off DNS answers first. Callers fall back to the bridge
address within one TTL. Then disable the listener and the host rules.
Addresses stay allocated, so re-enabling restores the same addresses.

## Alternatives

- **Platform-allocated port on the bridge address.** Simpler and needs no DNS
  change. But ports are not natural, every client needs an injected port, and
  the pool is finite and global.
- **Extending Gregale private networks (ADR-186..189).** Those carry real L3
  member addresses. Ingress is DNATed only to the app port. A parked target
  has no namespace, so a SYN goes nowhere and there is no wake path. The
  fabric is also gated on an operator overlay.
- **TPROXY.** It keeps the original destination as the local address, but
  needs `CAP_NET_ADMIN` in gatewayd-internal.
- **Protocol sniffing (SNI, startup packets).** Works for a few protocols
  only, and needs per-protocol parsers on the trust boundary.
- **One global address per app.** Exhausts a shared block across tenants and
  leaks existence across accounts.

## Evidence required

- Renderer golden output plus `make egress-render-cross-check` for the host
  prerouting chain. Netns rules order the service-address admission ahead of
  the ADR-361 drops.
- Store conformance: no two live apps in an account share an index. Reuse
  happens only after the quarantine.
- Native metal `TestServiceTCPMetal`:
  - a declared-port round trip to a parked target, same-node and cross-node;
  - cross-account and undeclared-port connections reset;
  - HTTP on `:10081` via the service address still works;
  - `make leakcheck` clean.

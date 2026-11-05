# ADR-20261104160000 · Fence DNS grants and revoke peerings before repair

- **Status:** accepted
- **Date:** 2026-11-04
- **Amends:** ADR-373 (DNS egress), ADR-20261026150000 (database egress), ADR-20261104001001 (private routing)

## Context

A DNS reply may arrive after its caller's source address has been recycled.
Applying permissions by source address alone can grant the replacement VM access
to the previous app's bound database. Seeding a new VM also renews cached grants
to the minimum DNS TTL instead of preserving their remaining lifetime. Peering
revocation currently waits for unrelated fabric and router endpoint repairs, so
a failed repair can leave deleted peerings forwarding traffic.

## Decision

Capture the live instance ID, app identity and an opaque network generation
through ResolveServiceCaller before querying upstream DNS. Occupied builder and
job slots receive a generation without an app identity. Capture strings by value
and close the client lease before resolution. Use the captured app for database
binding lookup, then include the original instance ID and generation in
AllowResolvedEgress. vmmd checks both against the current source owner before
any mutation. A new Instance or vmmd restart mints a new generation. Missing
proof fails closed. DNS grants and seeding join the existing circuit/teardown
mutex; pending cleanup refuses new grants, and teardown waits for an already
admitted grant before recycling its namespace.

Seed cached DNS and database entries using each original absolute expiry. Render
only its remaining whole seconds; omit expired entries and entries with less than
one second left. Fresh DNS answers retain the existing TTL bounds.

Add a separate RestrictPrivateNetworkPeerings control RPC. Before repairing any
regional fabric, schedd calls it on every active regional node, collecting
failures without skipping other nodes. vmmd atomically installs the intersection
of previously applied and desired bidirectional routes. An unknown prior policy,
including after restart or namespace recreation, starts at default drop. CIDR
changes require both directions to match the previous policy. Restriction never
repairs links or adds permissions. An empty desired policy completes without
fabric repair. Nonempty policies proceed through repair and full activation
only after all restrictions succeed. The normal reconcile RPC also restricts
before local repair, protecting callers using that method directly.

An errored restriction discards its cached prior permissions: the kernel may
have committed the removal before the error arrived. A retry must not restore
those unconfirmed permissions in its restrictive phase.

## Compatibility and rollout

All protocol additions retain existing field numbers and methods. The ownership
proof deliberately changes admission behavior: update vmmd and gatewayd-internal
as a coordinated node pair. A mixed pair still answers DNS but cannot install
new fenced egress permissions; existing kernel entries expire normally. There
is no unsafe source-only fallback.

Upgrade regional vmmds before schedd. An older vmmd returns Unimplemented for the
new restriction RPC; schedd reports failure and does not activate new peerings.
Other reachable nodes still receive revocations. Existing legacy nodes require
upgrade to enforce the revised revocation behavior; an error does not claim
regional convergence. The restriction method is absent from the gateway data
socket allowlist and retains the existing control transport boundary.

## Validation

Regression tests cover reused source addresses and instance IDs, immutable
caller capture before DNS resolution, bound database attribution, non-app slots,
missing ownership proof, original DNS/database expiry, complete and partial
revocation during unrelated endpoint failure, CIDR changes, restart defaults,
and restriction ordering and failure across multiple nodes. Native kernel and
VM lifecycle acceptance still require the dedicated Linux KVM hosts.

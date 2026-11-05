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

## Amendment (2026-11-05): running builders and adopted guests keep their DNS grants

Release rc.324 refused every builder's DNS grants, so no build on the node
could reach a registry. builderd waits for a build by calling vmmd's Destroy
as soon as the builder boots. Destroy records its cleanup intent, then the VMM
waits for the builder to exit by itself; that intent refused every grant for
the whole build. Refusals were silent on both sides.

Cleanup intent no longer refuses grants while the VMM waits for the guest to
exit by itself: a builder, or any teardown with an export target. The waiting
call clears that state as soon as the guest has exited, before cleanup
proper. From then on grants are refused as before, and cleanup still waits
for an admitted grant before recycling the namespace. Stops and kills, which
do not wait for a natural exit, refuse grants from the moment cleanup intent
is recorded, as before.

An adopted guest (vmmd restart, release or rollback) is rebuilt from its
adoption record, which does not carry the DNS gate or the bound-database set.
The adopted Instance looked ungated, so every grant was skipped while its
namespace kept dropping unresolved addresses; after the rc.322 rollback on
fsn-2 the adopted VMs' egress_resolved sets stayed empty. Adoption now
restores both for every networked guest, the gate following the node's
current ADR-373 switch as a prepared namespace does. The record format is
unchanged, so an N-1 vmmd still reads it.

vmmd logs each refused grant with the check that refused it (owner not
captured, instance not live, teardown started, source address changed,
network generation changed), once per instance and reason per minute. The
bridge resolver treats a refused grant as a warning, once per guest per
minute.

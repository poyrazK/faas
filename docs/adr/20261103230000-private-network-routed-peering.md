# ADR-20261103230000 · Isolate private-network peering routers

- **Status:** accepted
- **Date:** 2026-11-03
- **Amends:** ADR-20261103194727 (runtime network enforcement)

## Context

Installing a remote private subnet as an on-link route makes a member ARP for
remote addresses on its original bridge. A VXLAN per network extends that
network's L2 domain, but does not connect two different networks. Root namespace
routing also cannot distinguish accounts that reuse the same private CIDRs.

Static egress notifications target live nodes. A fresh node therefore has no
process-local reservation, and a node can miss a pin clear while the app is
parked. Those caches cannot authorize or reject the next production wake.

## Decision

Each account and region has a deterministic router network namespace with a
gateway veth on each of its network bridges. The existing first usable address
is the gateway. Bridges stay L2-only; reconciliation removes the legacy root
gateway address. The existing network-specific VXLAN carries same-network
traffic between nodes, and every regional node prepares all router endpoints.
Members route their own subnet directly and remote peered subnets via that
gateway. No address translation occurs in the router.

An inet firewall in the router drops input and forwarding by default. Each
authorized peering contributes two directional interface/address-range accepts.
There is no general established-flow exemption: replacing the complete policy
also revokes existing peering flows. Unpeered networks cannot use a third network
as transit. Namespaces isolate routing and conntrack between accounts and regions;
existing durable validation rejects overlapping networks within one account and
region.

Host policy permits forwarding only within the same dedicated private bridge
when br_netfilter invokes the root forward hook. An early root notrack rule keeps
those bridged packets out of the shared host conntrack domain. Per-workload and
account-router firewalls still apply in their own namespaces. Root policy never
permits cross-bridge private routing.

Schedd prepares every network fabric on each active regional node, then sends
the complete account/region peering policy through the additive
`ReconcilePrivateNetworkPeerings` RPC. Unsupported nodes or failed mutations keep
peering convergence unsuccessful. Only after all nodes acknowledge does schedd
publish member routes. Empty policies revoke all peerings. Authoritative sweeps
do not union older ready rows back into their desired routes. Ordinary attachment
replays retain their ready-peering overlay.

Fabric caches are planning hints. Capture-capable runtimes probe links on every
replay, repair missing router/veth/VXLAN links and memberships, and reconcile the
observed VXLAN flood list with explicit `del` and `append` operations. Linux rejects
`replace` for the zero-MAC VXLAN flood entry. Deletion removes router endpoints
and deletes an empty router namespace after observing its remaining links.

Vmmd reads current durable static-egress intent for every production wake and
checks the dispatch against that intent. An authorized pin restores the local
reservation and bridge alias before boot; a current clear releases stale local
reservation state. Account ownership and operator provisioning remain mandatory.
Live updates recheck durable intent under the app mutation gate; a periodic
reconcile repairs missed notifications. Stage exclusions remain explicit.

Allowlist shape changes use the existing complete atomic policy replacement,
including live DNS/database grants and circuit state. Successful acknowledgements
require the live policy and host projection to apply. Omitted firewall-rule CIDRs
retain the owning subnet as their default even when peering adds more routes.

## Validation and rollout

Portable regressions cover routing next hops, explicit pair authorization,
account/region isolation, revocation, unsupported nodes, link repair, allowlist
transition failures/retries, and static-egress wake/clear/reconciliation.
Default host renderer parity remains unchanged when no private fabric is present.

`TestPrivateNetworkRoutedPeeringKernel` is an opt-in Linux test that reexecutes
inside fresh network and mount namespaces, with its own `/run/netns` and proc
mounts. It exercises the real Manager, nftables, routes and bridges: bidirectional
traffic, unpeered denial, overlapping account CIDRs, established TCP revocation,
and router teardown. Its separate VXLAN subtest skips when the driver is absent.
Enable it with `FAAS_PRIVATE_NETWORK_KERNEL_TEST=1` on a host or isolated container
with `CAP_NET_ADMIN`, `CAP_SYS_ADMIN`, iproute2, nftables and ping.

Upgrade vmmd before enabling routed peering on schedd. Existing per-network
fabric reconciliation migrates gateway placement and repairs endpoints. Native
packet-flow acceptance on a privileged Linux host with VXLAN and KVM must also
verify cross-node TCP through real workload namespaces. The development kernel
passed the isolated routed-peering test, including established TCP revocation,
but lacks VXLAN and KVM for that remaining acceptance gate.

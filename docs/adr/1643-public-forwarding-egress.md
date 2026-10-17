# ADR-1643: public forwarding crosses the funded interface

- **Status:** local implementation candidate; dedicated native acceptance required.
- **Extends:** ADR-028, ADR-047 and ADR-1642.
- **Decision:** native-recovery VMMD public HTTP, Upgrade, TCP and UDP bridges connect
  from the host through the instance's public veth identity. Authorized private
  service calls retain their namespace-local bridge.

## Problem

The ADR-028 namespace-local dial to `10.0.0.2` delivers guest HTTP replies to
the namespace's local stack. Those replies do not cross public-peer egress or
root veth RX. Direct packet tests and a positive ARP counter therefore cannot
prove either response metering or strict byte enforcement for this route.

## Routing and accounting

Keep one authenticated VMMD gRPC listener and the identical guest network.
Public bridge processes on these owners run in the host namespace and dial the allocated
public identity. Requests enter the public veth peer and are DNATed to the
guest. Replies pass TAP segmentation, the existing funded quota and deadline,
then root veth RX, which remains the sole billing quantity. Bridge/grpc bytes
and gateway `tx_bytes` never become an additional invoice quantity.

The stable public `:8080` still targets the main workload's actual app port.
For the node bridge source only, swap the main port with `:8080` and preserve
other TCP ports; UDP ports are preserved. This permutation supports a main
workload on a custom port alongside an actual guest `:8080` sidecar, without
another host listener or port allocator. Added rules require the root bridge
source, public peer and exact allocated destination. Other sources retain the
existing published main port. Qualification candidates retain their narrower
ingress policy. Retargeting rebuilds the same complete DNAT chain atomically.

Capture the original root veth interface index during native network setup,
before bridge prewarm and live publication. Install a host `/32` route on that
owned veth and a permanent neighbor from the actual namespace peer MAC;
both are removed automatically with the device. ARP received through a bridge
updates the master, so it cannot resolve a slave-specific route on its own.
Read the peer and install the neighbor under the original native helper
authority before prewarm/publication. Every public socket uses
`SO_MARK` with the reserved high bit and original ifindex, pins the root bridge
source, and checks the interface before and after connect. A stateless host
output guard XORs the mark with actual output ifindex, drops a mismatch, and
restores the mark for accepted packets. It covers every TCP/UDP packet,
including retransmissions and existing pool sockets. Receive remains unbound
to the slave: Linux delivers replies through `br-tenants`.
A retired/reused interface fails closed, including persistent H1/H2C pools;
no public error falls back to the namespace-local dial. Established sockets
remain subject to their original namespace's quota and deadline. Native owner
restart retires the old network and credits through the existing recovery
contract. The v1 rollback gets a checked, connected descriptor from VMMD;
its shell cannot reopen the guest's namespace-local socket.

Native setup atomically installs the stateless `inet faas_public_forward`
guard before preparing a network. The ordinary Go/Jinja host-policy renderers
retain the same table across full-policy reloads when native forwarding is
enabled (`HostPolicy.PublicForwarding`, renderer `--public-forwarding`, Ansible
`faas_public_forwarding: true`). Legacy policy renders omit the new table.
VMMD enables the renderer field with `native_process_recovery`. It is persistent host policy,
not a per-VM quota or per-VM resource; it contains no dynamic entries to leak or
reset. Reserve socket mark bit `0x80000000` for these bridges. Variable bitwise
expressions require current nftables/kernel support (nftables 1.1.3+ and Linux
6.13+); an unsupported transaction refuses native boot. Matching host policy
is part of the immutable release/rollback contract. `IP_UNICAST_IF` alone is
insufficient for TCP and binding receive to a bridge slave is unsuitable.

The root bridge requires loose or disabled reverse-path filtering for the
device-specific output route and bridge-master receive path. A native wake
refuses publication if the effective `rp_filter` is strict or cannot be read;
it does not modify global host configuration. The route is created under the
existing native network ownership and cleaned with its veth. Private helpers
do not receive the public target or output pin.

Alternate/test VMMs and legacy owners without native process recovery retain
their existing namespace bridge. They cannot attest strict egress readiness;
the financial activation gate still requires capable native-recovery owners.

## Private service calls

The gateway marks the context only after private service authorization and
endpoint selection. At the VMMD RPC boundary, it emits authenticated gRPC
metadata `gregale-private-forward: 1`. Customer HTTP headers cannot select
this route, and the forwarder replaces inherited outgoing metadata with the
context's decision. Missing, duplicate or unknown values select public
forwarding. The existing mTLS/Unix peer authorization remains mandatory.
Private HTTP/Upgrade and service TCP use this marker. Public and private
persistent bridges have separate sockets and pool entries; lifecycle cleanup
removes both. Private side-link traffic remains outside the public meter.

## Acceptance and rollout

Portable tests cover port permutations, node-only publication, stale/replaced
interfaces at connect, invalid authority, private-marker trust, public/private
pool isolation and all helper dial sites. Native qualification must compare
actual complete/streaming response payload against interface-byte deltas and
issued credits for both public HTTP bridge versions, concurrency, exhaustion,
deadline and restart. A private request after public exhaustion must not add
response payload to the public counter. Recorded source/host/evidence identity
and leakcheck remain mandatory. Compilation does not qualify a host or make
strict budgets production-ready. All peer helper binaries must come from the
same rebuilt immutable bundle; mixing old namespace-only bridge binaries with
the new owner is unsupported.

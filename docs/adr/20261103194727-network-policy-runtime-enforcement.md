# ADR-20261103194727 · Preserve network policy across runtime transitions

- **Status:** accepted
- **Date:** 2026-11-03
- **Amends:** ADR-119 (static outbound IP), ADR-186 (network CIDR policy), ADR-187 (network firewall rules)

## Context

Network policy must survive scale-to-zero wakes, peering replays, provider
readiness changes, and public-listener refreshes. The runtime used different
projections for these paths: wakes omitted the network CIDR baseline, peering
replays omitted its firewall, and a failed provider check changed status without
withdrawing live routes. Public raw listeners also retained ports later marked
internal.

Static egress matched a reserved app address from `10.200.0.0/16`, although the
namespace MASQUERADE actually uses each VM's lease address in the tenant bridge
subnet. That rule cannot match outbound packets. A subnet-specific SNAT rule
placed before the broad MASQUERADE works even when both match the same subnet.

## Decision

Use one CIDR policy merge for private-network attachment reconciliation, wake
projection, and peering replay. The network supplies the reusable baseline and
firewall rules; attachment policy can only narrow that baseline. Failed checks
withdraw live private routes, including private side-links, and retry failed
withdrawals on subsequent sweeps. Pending/error attachments remain disconnected
until readiness and route installation succeed again.

Live private-policy replacements delete and recreate both nftables tables in
one transaction. The same transaction restores unexpired DNS and bound-database
permissions and open circuit targets. Per-instance DNS writes serialize with
replacement, and app policy mutations serialize with wakes. Rule handles are
refreshed after replacement. Failed transactions retain the previous policy.

Identify the attachment subnet by its member address before validating the
firewall or configuring the side-link; sorted peer destinations cannot replace
that subnet. Periodic peering batches select account/region groups and load
their complete desired state. Successful ready rows also advance their reconcile
timestamp so they cannot permanently occupy the oldest-first queue.

Private ingress filters account for prerouting DNAT: CIDR-only accepts use the
translated guest port, and protocol rules match the original published port.

Public TCP/UDP inventory excludes a protocol/port marked internal in the current
manifest, even if the listener's name is stale. Admission also checks current
visibility, including after a cold wake. Inventory refresh then withdraws its
accepting socket and existing sessions through the existing supervisors.

Build static SNAT rules from every live or starting VM's actual lease source,
combined with its app's desired customer IP. Install the projection before
customer code boots, and remove prospective sources when boot fails. Teardown
removes only the departing source. Replaying the same pin retries a failed host
reload. Operator bundle reloads manage aliases and provisioning; they cannot
replace runtime SNAT rules with entries lacking VM sources. Existing reservation
and API formats remain compatible; reservation addresses are not dataplane
source addresses.

The additive `static_egress_excluded` wake field explicitly excludes stages
from production static egress. Its default keeps older callers eligible.
vmmd retains the exclusion on the instance and in adoption records; a stage
wake cannot overwrite the production pin, and later app-level pin updates
cannot add a stage's source to host SNAT. Both cold and restore adapters carry
the pin and exclusion.

On nodes using the optional tenant egress gateway, mark traffic from these
specific pinned sources with `0x40000000` in host prerouting. vmmd probes and
idempotently installs a bridge-scoped IPv4 routing rule at priority `100` for
that mark/mask to consult `main`, ahead of the gateway's `10000+` rules. The
host firewall admits those sources on the public interface after its destination
and SMTP denies. Unpinned tenants retain the gateway route and its fail-closed
behavior. The routing rule is inert when no runtime source is marked, including
after a restart or pin removal. Its mark is separate from the public bridge
socket guard's output hook and `0x80000000` flag.

## Validation

Portable regressions cover private policy projection and replay, route withdrawal
failure/retry/recovery, TCP/UDP admission, PostgreSQL listener inventory, and
static-egress boot/concurrency/teardown/reload behavior. Host renderer checks
cover SNAT ordering and the scoped gateway exception. Native KVM and kernel
packet-flow acceptance remain required on the dedicated Linux acceptance host.

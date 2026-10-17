# ADR-1363: shared account compute and egress funding

- **Status:** local implementation candidate; native acceptance required.
- **Extends:** ADR-1361, ADR-1362 and ADR-046.
- **Decision:** support account `strict`, `net_usage`, `suspend_workloads`
  budgets with meters `[compute, egress]`, zero drain and no alert thresholds.

## Financial authority

Compute MB-seconds and interface egress bytes have separate maximum-period
quantity allowances and conservative monetary books. Every matching policy is
checked under the existing account lock. A combined policy compares the SUM
of both books' committed and reserved money with its single limit. A parallel
compute-only policy still compares compute alone. Every app, worker, command,
execution, sidecar and independent job uses the existing physical attempt lease.
Builds, object-storage delivery, subscriptions, storage and tax remain excluded.

Byte credits are bought before network activation or refill. Each credit is
bounded by the operational quantum in limits.go and may shrink to the remaining
allowance or paid headroom. Credits remain reserved until physical retirement,
then commit in full, including unused or uncertain delivery. The budget book
therefore can exceed actual invoice usage; it never produces an extra invoice
charge. This deliberate conservative loss avoids refunding a destroyed kernel
counter or reissuing credits after a lost response. Negative corrections cannot
mint funding. A grant increase may reduce future reserves, not past commitments.

Initial activation and first-period preparation require quiet workloads, complete
compute AND egress evidence, and current byte-enforcement capable owners. Retained
positive usage, including deleted resources, seeds both books. The largest
recorded grant per meter is retained. All retained egress price versions must
be EUR/interface_bytes. The maximum recorded monthly egress rate is pinned;
price writes above that ceiling are refused for the funded period, even after
policy deletion. Pricing changes require a compatible period transition.

## Kernel boundary and recovery

A netdev egress chain on the namespace's PUBLIC veth peer gates all packets
before root-side veth RX, the canonical billed interface counter. Install it
while the peer is down, before IPv6 autoconfiguration, ARP or guest execution.
It covers established connections, HTTP responses, IPv4, IPv6 and ARP. Malformed
frames shorter than 20 bytes and unsupported Ethernet protocols are dropped.
Private side-link traffic stays outside this gate, as it is outside this meter.
The quota gets half the financed interface bytes: the factor of two covers
Ethernet/VLAN framing even when nft counts only network bytes. A separate kernel wall-clock rule expires traffic before the funded UTC
deadline, even if a daemon dies or VM retirement fails. Native nodes require
synchronized clocks; authority responses validate skew and the expiry includes
twice the RPC timeout as an early margin. Time-only renewal changes this expiry
without resetting the byte quota. Quota accounting
is atomic; the packet crossing the quota is dropped. No polling overshoot is
permitted. Kernel support for netdev egress quotas is required; setup failure
prevents boot. Native packet tests must verify the bound against veth RX.

Refill observes the canonical cumulative interface counter for low-water only;
it grants no authority. The DB buys a fresh credit first, then the native owner
atomically replaces the exhausted/low quota. Any previous unused credit is lost
conservatively. A replacement is applied once under the attempt lock. An unknown
DB or kernel outcome stops renewal; it is never replayed into a fresh quota.
Counter regression also stops renewal. The kernel gate remains closed at its
credit boundary independently of daemon, scheduler, DB or meterd availability.
A restarted owner retires the original attempt; it never resets or resumes its
old quota. Native generation joins fence helpers, teardown and allocator reuse.

A separate egress incarnation attestation fences older binaries. DB lease writes
refuse unfunded or incapable attempts while a combined policy is enabled, even
from legacy financial writers. Previously attested owners cannot downgrade their
incarnation while retaining a false capability proof. Database guards also retain byte book quantities and lease fields across
legacy serializers, including after policy deletion; the pinned period rate
cannot be overwritten. Migrations are forward-only.

## Acceptance

Require MemStore/Postgres conservation and contention, negative receipts,
price ceilings, policy edits, restart fencing, failed settlement and month
boundaries. Portable tests check gate placement and once-only refill. Dedicated
native tests must cover packet-sized exhaustion, concurrent senders, IPv4/IPv6,
ARP, HTTP/WebSocket replies, private side-link exclusion, actual veth RX equality
or upper bound, failed kill, DB disconnect and daemon restart, followed by
leakcheck. Compilation does not satisfy these release gates.

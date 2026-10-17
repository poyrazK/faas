# ADR-1642: shared account compute and egress funding

- **Status:** local implementation candidate; native acceptance required.
- **Extends:** ADR-1640, ADR-1641 and ADR-046.
- **Decision:** support account `strict`, `net_usage`, `suspend_workloads`
  budgets with meters `[compute, egress]`, zero drain and no alert thresholds.

## Financial authority

Compute MB-seconds and interface egress bytes have separate quantity allowances
and conservative monetary books. Compute retains its maximum-period grant.
Egress funding pins the lowest recorded monthly grant at activation; later
increases benefit funding in a new UTC month. Lower egress grants are refused
for an already funded period. This bounds provider delivery even when previous
hours were invoiced before a grant increase. For any hourly grant G_i >= the
pinned G, each net quantity delta is bounded by the delta at G, so the sum is
bounded by max(total interface bytes - G, 0). Retained invoice allocation can
use a larger grant; the funding book deliberately remains conservative. Every matching policy is
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
mint funding. Compute grant increases may reduce future reserves; egress terms remain pinned.

Initial activation and first-period preparation require quiet workloads, complete
compute AND egress evidence, and current byte-enforcement capable owners. Retained
positive usage, including deleted resources, seeds both books. Compute retains its largest
recorded grant; egress funding keeps its conservative pinned grant. All retained egress price versions must
be USD/interface_bytes. The maximum recorded monthly egress rate is pinned;
provider delivery also checks the retained rate, allowance and total funded byte
quantity before sending an event. Incompatible pricing, quota downgrades or
excess sampled quantities stay pending without provider events or receipts.
Price writes above that ceiling are refused for the funded period, even after
policy deletion. Pricing changes require a compatible period transition.

## Kernel boundary and recovery

A netdev egress chain on the namespace's PUBLIC veth peer gates all packets
before root-side veth RX, the canonical billed interface counter. Install it
while the peer is down, before IPv6 autoconfiguration, ARP or guest execution.
It covers established connections, HTTP responses, IPv4, IPv6 and ARP. Malformed
frames shorter than 20 bytes and unsupported Ethernet protocols are dropped.
Private side-link traffic stays outside this gate, as it is outside this meter.
Guest TAP ingress first traverses a namespace-local IFB with segmentation and
aggregation offloads disabled. Transmit validation splits GSO aggregates before
they return to TAP ingress and reach the public-peer quota. This prevents a GSO
aggregate from expanding into additional billable headers after quota accounting.
IFB/ethtool/TC setup is mandatory and precedes TAP activation; any failure refuses
boot. The IFB and its qdisc die with the original owned namespace.
The quota gets half the financed interface bytes: the factor of two covers
Ethernet/VLAN framing even when nft counts only network bytes. A separate kernel wall-clock rule expires traffic before the funded UTC
deadline, even if a daemon dies or VM retirement fails. Native nodes require
synchronized clocks; authority responses validate skew and the expiry includes
twice the RPC timeout as an early margin. Time-only renewal changes this expiry
without resetting the byte quota. Quota accounting
is atomic; the packet crossing the quota is dropped. No polling overshoot is
permitted. After policy removal, existing funded attempts continue to obtain uncapped
credits through their original gate; new unbudgeted workloads remain billable.
Delivery quantity bounds apply while an account cap is active. Retained period
pricing and allowance checks survive deletion. Kernel support for netdev egress quotas is required; setup failure
prevents boot. Native packet tests must verify the bound against veth RX.

Refill observes the canonical cumulative interface counter for low-water only;
it grants no authority. The DB buys a fresh credit first, then the native owner
atomically replaces the exhausted/low quota. Any previous unused credit is lost
conservatively. A replacement is applied once under the attempt lock. An unknown
DB or kernel outcome stops renewal; it is never replayed into a fresh quota.
Counter regression also stops renewal. A pre-boot financial authority
refusal defers a claimed job without spending an execution retry, including
the stable gRPC error form; destruction must join before releasing the claim. The kernel gate remains closed at its
credit boundary independently of daemon, scheduler, DB or meterd availability.
A restarted owner retires the original attempt; it never resets or resumes its
old quota. Native generation joins fence helpers, teardown and allocator reuse.

A separate egress incarnation attestation fences older binaries. DB lease writes
refuse unfunded or incapable attempts while a combined policy is enabled, even
from legacy financial writers. Previously attested owners cannot downgrade their
incarnation while retaining a false capability proof. Database guards also retain byte book quantities and lease fields across
legacy serializers, including after policy deletion; the pinned period rate
cannot be overwritten. Migrations are forward-only. The delivery-terms migration refuses pre-existing
egress candidate books rather than silently repricing earlier issued authority;
these unpublished candidates require explicit forward repair if already applied.

## Acceptance

Require MemStore/Postgres conservation and contention, negative receipts,
price ceilings, policy edits, restart fencing, failed settlement and month
boundaries. Portable tests check gate placement and once-only refill. Dedicated
native tests must cover packet-sized exhaustion, concurrent senders, IPv4/IPv6,
ARP, HTTP/WebSocket replies, private side-link exclusion, actual veth RX equality
or upper bound, failed kill, DB disconnect and daemon restart, followed by
leakcheck. Compilation does not satisfy these release gates.

## Currency integration

New strict funding uses USD under ADR-1430. Historical EUR contracts and budget
revisions remain immutable and cannot authorize USD funding. The forward-only
currency migration refuses pre-release funding books and enabled non-USD
policies instead of converting retained balances. Incompatible historical
contracts in the same UTC period keep strict readiness unavailable.

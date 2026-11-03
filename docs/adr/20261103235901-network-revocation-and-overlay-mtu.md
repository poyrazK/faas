# ADR-20261103235901 · Preserve revocation cues and private packet size

- **Status:** accepted
- **Date:** 2026-11-03
- **Amends:** ADR-119, ADR-20261103194727, ADR-20261103230000

## Context

Deleting the final peering erases the periodic worker's discovery key. A
separate failed notification enqueue can therefore leave forwarding authorized
indefinitely. Globally limited attachment and peering queries also omit tenants
before filtering their scope, while still reporting successful convergence.

Concurrent static-egress wake reads can return out of order. An older empty pin
must not erase a newer accepted pin, reservation, or host rule. Operator files
also need ownership over their database grants: removing the final entry must
revoke it without deleting another node's provisioning. A revoked grant cannot
silently turn the customer's pinned app into an unpinned app.

Private veths originally kept MTU 1500 while a VXLAN port over a smaller
encrypted underlay could carry less. Bridging drops oversized frames without a
routed PMTU response, so small checks succeed while real payloads stall.

## Decision

Commit peering deletion and its account/region revocation outbox entry in the
same PostgreSQL transaction. Failed enqueue rolls back deletion; successful
handoffs retain existing durable delivery/retry semantics after the row vanishes.
In-memory test adapters retain their existing notifier seam.

Use scoped attachment pages ordered by immutable app ID, with a keyset cursor.
Filter account, region and optional network in the database before limiting.
Peering overlays load the owning account/network's relationships and then select
ready rows. Keep periodic work batches bounded, while event-driven convergence
walks its entire affected scope.

Each static wake receives a monotonically increasing local read generation.
Only a short per-app commit fence covers reservations, alias checks and cache
publication. A delayed older result cannot replace a newer accepted different
pin; its caller retries with current intent. The fence does not cover boot.
Register every eligible starting lease for host projection, including wakes
that originally requested no pin, so a later accepted pin also covers them.

Add a separate node-owned provisioning grant table, preserving the legacy gate
and its key without a live index rebuild. A stable compute node ID identifies
the writer; the legacy local daemon uses `default-local`. A bundle publication
atomically replaces that source's complete set, including an empty set. Grant
existence remains an account/IP lookup across sources. The old per-account
setter writes only the legacy table. Scoped publication retires unowned legacy
rows conservatively, then installs current node-owned grants. Periodic publication
repairs database failures and a disabled file clears that node's source too.

The local operator file fences alias installation by account/IP. Its app label
does not prevent moving a provisioned address between apps in the same account.
Revoked provisioning releases the local reservation and removes SNAT, while a
host forward rule blocks that production app's outbound original-direction
traffic before established-flow accepts. It preserves customer intent and enforces
a known local revocation before a database read can consume the repair deadline.
Excluded stages and inbound replies keep their existing rules. Its adoption record retains the
static-policy refusal marker, including while provisioning is revoked. Reprovisioning reinstates SNAT and
removes the block; an explicit customer clear restores ordinary egress.

Register separate SIGHUP channels for the allowlist and static-IP reloaders.
Signal registrations broadcast; a shared channel does not.

Use a common private-fabric MTU of 1230 by default, accounting for 50 bytes of
inner Ethernet and outer IPv4/UDP/VXLAN headers over a 1280-byte encrypted interface. An operator can
set a common regional override with `private_network_transport_mtu` or
`FAAS_PRIVATE_NETWORK_TRANSPORT_MTU`; every node must use the same value.
Reject reconciliation when the local underlay cannot carry it plus overhead.
Set the MTU on the bridge, VXLAN link, router veths and both workload private
veth ends, including existing links during live reconciliation. Report observed
bridge/transport MTU drift as not ready.

Keep the guest's fixed network and snapshot contract unchanged. Its namespace
router supplies PMTU feedback at the private interface. Clamp TCP SYN and
SYN-ACK MSS downward in both private directions, before established-flow accepts,
so large transfers work without waiting for a lost packet or PMTU discovery.
Persist the private MTU in adoption records for subsequent policy replacement.

## Rollout and validation

Apply the additive provisioning migration and upgrade apid readers before vmmd. Upgrade provisioning
writers and refresh their operator bundles before enabling static-egress
admission across a fleet: unowned legacy grants are retired and must be
republished with node ownership. Customer pin intent is retained. Existing
legacy reads and inserts remain valid against the unchanged original table.

Regressions cover out-of-order wake completion, local/database revocation and
recovery, independent bundle owners, rollback on failed outbox insertion, one
signal reaching both reloaders, and scoped convergence beyond 1000 rows.
The isolated Linux MTU test uses the real per-instance nftables/NAT renderer
and fixed-IP guest stand-ins across a reduced-MTU bridge port. It checks a large
bidirectional TCP transfer and MTU feedback for an oversized datagram. Full
cross-node VXLAN and Firecracker acceptance still require an equipped host.

# ADR-20261123100000: App calls to another node's entry address stay on the node

- Status: accepted
- Date: 2026-11-23
- Amends: ADR-20261103163954 (app hosts point straight at the nodes, §2 and
  §4)

## Context

With direct entry and nothing in front, `*.gregale.app` resolves to every
compute edge's entry address, and every edge serves every app. An app that
calls another app's public URL (a frontend calling its API, a webhook, an
identity provider hosted on Gregale) therefore reaches either its own node's
edge or another node's edge.

A call to the node's own entry address already stays on the node: the input
chain accepts the tenant bridge on 443 before the entry rules, and the edge
sees the guest's own bridge address. A call to another node's entry address
leaves the node masqueraded to the node's primary address. That breaks in
two ways, both found on OVH on 2026-11-23:

1. The provider network between dedicated servers is not reliable. bhs-2 and
   bhs-3 share a DHCP /24, so the serving node sent the reply on-link to the
   caller's primary address, and OVH answers ARP between the servers only
   sporadically. With an empty neighbour cache 20 of 20 connections failed;
   in practice about half of app-to-app calls hung. Routing the subnet
   through the gateway (host netplan, `GcpToOvhMigration.md`) fixes the
   hosts there today, but every new node needs it.
2. Every tenant on the calling node shares one source address at the other
   node's entry. The per-source backstop on the entry address (1024 open
   connections, 200 new a second, ADR-20261103163954 §4) and the edge's own
   per-address limits then count the whole node as one client: one busy
   tenant cuts off every other tenant's calls to apps on that node.

Exempting peer nodes' addresses from those limits would trade (2) for an
unlimited source that any tenant can use against another node's edge.

## Decision

1. **A guest's TCP/443 connection to another compute edge's IPv4 entry
   address is DNATed to this node's own IPv4 entry address** in the tenant
   bridge's NAT prerouting chain:
   `iifname "br-tenants" ip daddr { <peers> } tcp dport 443 dnat ip to <own>`.
   The local edge serves it as it serves a call to its own address: the
   connection never leaves the node, and the edge's per-address limits
   apply to the guest's own bridge address, per tenant.
2. The peer list is the manifest's other compute-only hosts' IPv4 entry
   addresses, rendered as `faas_peer_entry_addrs` for each compute edge
   with an IPv4 entry address and no front. Dynamically enrolled nodes get
   no entry address, so public DNS never names them and the list is
   complete. A node with no IPv4 entry address renders nothing; its calls
   leave the node as before.
3. The boot policy (nftables role) and vmmd's runtime policy
   (`HostPolicy.PeerEntryAddrs`, `FAAS_PEER_ENTRY_ADDRS` from the vmmd
   egress drop-in) render the rule byte-identically, as for the entry
   addresses themselves. Both refuse an IPv6 peer or the node's own
   address.
4. IPv6 is out of scope: `*.gregale.app` publishes no AAAA records.

## Consequences

- App-to-app calls over public URLs no longer depend on the provider
  network between nodes and are faster (no round trip off the server).
- Per-tenant limits hold for these calls; a busy tenant no longer starves
  its node's other tenants of calls to another node.
- The other node's edge never sees these calls. Anything an edge keeps per
  node (local-first instance choice, caches) sees them on the caller's
  node instead, as it already does for calls to the node's own address.
- The host route fix for shared subnets stays for traffic this rule does
  not cover (nodes without an entry address, host-originated traffic).

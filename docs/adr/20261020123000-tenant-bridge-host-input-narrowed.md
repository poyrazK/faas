# Guests reach only the bridge services and the public edge on the host

Date: 2026-10-20
Status: Accepted
Related: spec §11 ("nftables default-drop inbound"), ADR-169/384 (bridge service proxy), ADR-170 (bridge resolver), ADR-361 (egress ports), ADR-373 (DNS-gated egress), ADR-576 (private TCP services)

This record has a date-and-time identifier because date-only identifiers
collide under `make adr-number-uniqueness-check` when two ADRs land on the
same day.

## Context

The host `input` chain, both the Ansible template and the runtime
`HostPolicy.Render`, began with `iifname "br-tenants" accept`: every packet a
guest sent to any host address was admitted, on any port. The per-netns
forward chain blocks RFC1918, link-local and the other §11 ranges, and admits
the bridge address only on the service-proxy and resolver ports. Nothing
blocked the node's own public addresses.

A Pro or Scale app may declare up to eight extra egress ports (ADR-361), and
DNS gating (ADR-373) admits any address a name the tenant controls resolves
to. A guest could therefore open TCP to the node's public IP on a port such
as 9092 (gatewayd-public's control listener and its `/metrics`, bound to
`0.0.0.0` on edge nodes) or 9107 (realtime health). Those ports are closed to
the internet only by the rest of the input chain, and the guest never reached
those rules. A guest whose source address fell inside an operator CIDR on a
later `ip saddr` rule would match that rule too, if the bridge rule were ever
narrowed without a terminal drop.

## Decision

From the tenant bridge, the host `input` chain admits only:

1. destinations inside the tenant bridge subnet (`MasqueradeCIDR`,
   10.100.0.0/16): its only host address is the bridge IP, where the
   service proxy, the private TCP proxy and the resolver listen. The
   per-netns chain still limits which ports a guest can open there;
2. TCP 80 and 443 to any host address, so a guest can call a platform app
   through its public hostname when that resolves to this node (hairpin);
3. ICMP and ICMPv6, for path MTU discovery and IPv6 neighbor discovery.

Everything else arriving on the bridge is dropped right after these rules,
before the public and source-CIDR rules can admit it. Replies keep matching
`ct state established,related` at the top of the chain.

## Consequences

- A guest can no longer reach host listeners on the node's public or private
  addresses outside 80/443, whatever egress ports its app declares.
- Platform services that guests use must listen on the bridge IP; this is
  already true for every one of them.
- The rule depends only on values both renderers already have (the bridge
  name and `MasqueradeCIDR`), so no new deployment variable is needed.

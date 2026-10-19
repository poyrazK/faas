# Tenant egress denies the NAT64 prefixes

Date: 2026-10-18
Status: Accepted
Related: ADR-023 (IPv6 egress policy), ADR-034 (6to4 and Teredo deny), spec §11

This record has a date-and-time identifier because date-only identifiers
collide under `make adr-number-uniqueness-check` when two ADRs land on the
same day.

## Context

`pkg/netns.NewDefaultDenySet()` is the single tenant egress denylist: the
per-netns forward chain, the host forward chain and the user-space checks of
the OCI puller and webhook dispatcher all read it. ADR-034 added 6to4
(`2002::/16`) and Teredo (`2001::/32`) because both are IPv6 transition
mechanisms whose addresses embed an IPv4 destination: a guest that writes a
denied IPv4 address in the IPv6 form escapes the IPv4 deny rules, and a relay
puts the inner packet on the IPv4 network.

NAT64 is the same class of mechanism and was left out:

- **`64:ff9b::/96`**, the well-known NAT64 prefix (RFC 6052). The low 32 bits
  are the IPv4 destination, so `64:ff9b::a9fe:a9fe` is `169.254.169.254` and
  `64:ff9b::a00:1` is `10.0.0.1`. A NAT64 translator on the path, whether one
  the provider runs or one added later to give IPv6-only nodes IPv4 reach,
  turns the packet into IPv4 after our IPv4 rules have looked at it.
  RFC 6052 §3.1 forbids the well-known prefix for non-global IPv4 addresses,
  but that is a requirement on the translator, not something the tenant
  firewall can assume.
- **`64:ff9b:1::/48`**, the local-use NAT64 prefix (RFC 8215). It is meant for
  internal networks and may carry private IPv4 destinations.

Gregale's own outbound integration guard (`pkg/outbound/public_destination.go`)
and the custom-domain DNS check (`pkg/apid/apidserver/dns_verify.go`) already
treat these prefixes as non-public. The tenant firewall is the remaining
surface that does not.

No Gregale deployment uses NAT64 or DNS64: nodes have native IPv4, and tenant
IPv4 egress leaves through the IPv4 masquerade rule. Denying the prefixes
removes no working path.

## Decision

Add two IPv6 entries to `pkg/netns.NewDefaultDenySet()`:

| Prefix | Counter | Comment |
|---|---|---|
| `64:ff9b::/96` | `drop_v6_64_ff9b___96` | NAT64 well-known prefix (RFC 6052); embeds an IPv4 destination |
| `64:ff9b:1::/48` | `drop_v6_64_ff9b_1___48` | NAT64 local-use prefix (RFC 8215); may embed a private IPv4 destination |

Both prefixes are denied whole, as ADR-034 denies 6to4 and Teredo whole,
rather than only the sub-ranges that embed denied IPv4 space: a translator's
handling of the remaining space is outside the firewall's view, and the
platform has no use for either prefix.

Every consumer picks the entries up from the catalog: the per-netns and host
renders, the OCI puller and webhook user-space checks, the per-CIDR drop
counters and their `<daemon>_egress_deny_total{cidr,family}` series, and the generated
`docs/denylist.md`. The Ansible template
`deploy/ansible/roles/nftables/templates/policy_nftables.conf.j2` mirrors the
host render byte for byte and gains the same two counters and rules;
`make egress-render-cross-check` keeps the two in step.

## Consequences

- A guest can no longer reach an embedded IPv4 destination through a NAT64
  translator; the drop counters show any attempt.
- A future IPv6-only deployment that wants NAT64 for tenant IPv4 egress must
  supersede this record and replace the whole-prefix deny with denies of the
  embedded forms of every denied IPv4 range.
- Network-specific NAT64 prefixes (RFC 6052 §2.2) are operator-chosen and
  cannot be listed in a shared catalog. Running a translator with one would
  need its own entry; none is deployed.
- No wire, database, plan or API change.

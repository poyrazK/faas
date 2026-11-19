# ADR-20261109100000: Bare-metal hosts keep private services and sshd off the public card

- Status: Accepted
- Date: 2026-11-09
- Related: ADR-110 (split-box manifest), ADR-055 (host ruleset template),
  ADR-1344 §8 (OVH move), ADR-20261107203000 (off-host security evidence)

## Context

Gregale is moving from GCP VMs to OVH bare metal. On GCP each VM has one
private card, and two provider controls sit in front of it: the VPC firewall
and Identity-Aware Proxy, which is the only way to reach sshd. The host
ruleset (`roles/nftables/templates/policy_nftables.conf.j2`) relies on them:
it accepts `tcp dport 22` from anywhere, with the comment "provider
firewall/IAP applies the source boundary".

An OVH server has a public address on its main card and reaches the rest of
the fleet over the vRack, a second card. Neither GCP control exists there.
On such a host:

- sshd answers the whole internet;
- the overlay's `static` provider picks the default-route interface as the
  private card, which on bare metal is the public one;
- private services (Postgres, daemon ports, Loki) are admitted by source
  address alone, on whichever card the packet arrives.

## Decision

The manifest describes the boundary and the host ruleset enforces it. Both
are opt-in, so a cloud manifest renders the same ruleset as before.

1. `overlay.interface` names the private card for the `static` provider (for
   example `eno2`, or a vRack VLAN such as `eno2.4000`). The renderer emits it
   as `faas_overlay_iface` and `faas_private_iface`, and every private-service
   accept rule in the host ruleset gains `iifname "<card>"`. Raw TCP/UDP app
   listeners and the warm standby's replication stay as they are: the first
   are public by design, the second may cross sites.
2. A top-level `ssh` section restricts sshd. Present, sshd is admitted only
   from `overlay.cidr` (on the private card when one is named) and from
   `ssh.allowed_cidrs`, an operator bastion or VPN. An empty list means the
   private network only; `0.0.0.0/0` and `::/0` are refused. Absent keeps the
   provider boundary.
3. Before the ruleset restricts sshd, the nftables role reads the address the
   converge connects from and refuses to continue unless the new rules admit
   it, so no converge can lock itself out.

## Consequences

- An OVH manifest sets `overlay: {provider: static, cidr: <vRack CIDR>,
  interface: <vRack card>}` and `ssh: {allowed_cidrs: [...]}`. CD reaches
  hosts through `ansible_host`, the private address, so it stays admitted.
- Rendered output is byte-identical for every existing manifest (checked for
  the default, Cloudflare-origin, custom-domain and public-entry modes).
- OVH's network firewall in front of the public addresses is still worth
  configuring as a second layer; this ADR does not depend on it.
- Not covered here: daemons that bind `0.0.0.0` stay reachable only through
  the rules above. Binding each to its private address is follow-up work.

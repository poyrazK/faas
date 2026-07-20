# ADR-023 · IPv6 tenant egress policy

- **Status:** accepted
- **Date:** 2026-07-20
- **Decision:** Extend the tenant egress denylist (spec §11) to IPv6 by
  rendering a sibling `ip6 daddr { … } drop` line in the host firewall's
  forward chain and a per-netns `ip6` table+chain for the per-instance
  netns. Use the **family-keyword** form (`ip6 daddr`), not the
  `meta nfproto ipv6` wrapper. **Allow-and-restrict**, not disable-IPv6.
- **Why:** Spec §11 says "deny RFC1918 + link-local + metadata ranges" but
  the implementation was IPv4-only. The IPv6 equivalents are unblocked:
  `fe80::/10` (link-local, exposes the host's neighbor table to guests),
  `fc00::/7` (ULA, breaks the "no lateral movement into the control
  plane" promise), `ff00::/8` (multicast, no use case), `::1/128`
  (loopback), and `::/128` (unspecified source — misconfigured or
  malicious). This is the open issue #32 and an explicit §11
  ship-blocker.
- **Consequences:**
  - `HostPolicy` gains a `ForwardDenyIPv6CIDRs []string` field,
    populated in `DefaultHostPolicy` with the list above. The list
    mirrors `pkg/oci/egress.go::deniedCIDRv6` so user-space and firewall
    enforcement stay in lockstep; keep the two lists identical when
    editing either.
  - `HostPolicy.Render()` emits a new `ip6 daddr { … } drop` line in
    the existing `forward` chain, immediately after the `ip daddr { … }
    drop` line. The host-level table is `table inet faas` — already
    inet-family — so `ip6 daddr` slots into the same chain with no new
    table. No `meta nfproto` wrapper; the inet-family table evaluates
    IPv4 and IPv6 matches independently and the wrapper would only add
    verbosity. `TestHostPolicyRenderDeniesIPv6LinkLocalAndULA` enforces
    both the literal entries and the absence of `meta nfproto`.
  - The per-netns ruleset (`Config.NftCommands`) gains a parallel
    `ip6` table because nft rejects mixing `ip` and `ip6` matches in
    one `ip`-family table. The v4 chain handles IPv4 egress; the new
    `ip6 faas` table handles IPv6 egress with the same set of deny
    CIDRs. The `forward` chains both default-accept and both scope the
    deny to `iifname tap0` so the inbound DNAT path is never affected.
    An `established,related` accept is added first (same rationale as
    the v4 chain — published replies traverse the deny range).
  - The `pkg/oci/egress.go::deniedCIDRv6` list is now the single source
    of truth for the IPv6 deny set. The netns `ForwardDenyIPv6CIDRs`
    literal duplicates it for the firewall layer; if either changes,
    change both.
  - Outbound IPv6 to public addresses (Cloudflare 1.1.1.1, GitHub IPv6,
    modern CDNs) still works — the deny is restricted to the
    link-local/ULA/multicast/loopback/unspecified set, not a blanket
    IPv6 drop.
- **Rejected alternatives:**
  - **`meta nfproto ipv6 ip6 daddr { … } drop`** — adds an explicit
    family guard. Redundant in an `inet` table: IPv6 packets don't
    match `ip daddr` either. Matches the v4 line's format and the
    existing `iptables-nft` (Debian 12 default) rendered output. The
    `pkg/netns/policy.go` `ip daddr` line already follows this
    convention; matching it keeps the file visually consistent.
  - **Disable IPv6 entirely (`sysctl net.ipv6.conf.all.disable_ipv6=1`
    or a blanket `ip6 drop` rule)** — spec §11 demands deny-list
    semantics, not a blanket IPv6 drop. Disabling IPv6 breaks SLAAC
    and any future dual-stack listener on apid. A subset of paying
    customers will want IPv6.
  - **Fold per-netns IPv6 into a single `inet faas` table** —
    architecturally cleaner but requires nft's `ip6`/`ip` keyword in
    the same table, which works for `inet` but breaks the existing
    NAT ruleset (the prerouting/postrouting nat chains are `ip` family
    today and an `inet`-family rewrite touches every v4 rule). Kept
    the table-family split; this is a separate refactor.
  - **Share the CIDR list via a sub-package constant instead of
    duplicating** — adds a `pkg/policy` package or a one-liner in
    `pkg/api`; not worth the import path for two consumers. A
    code-comment cross-reference is the right coupling at this scale.

## Wire format (canonical)

Host `forward` chain (the relevant portion):

```
chain forward {
  type filter hook forward priority 0; policy drop;
  ct state established,related accept
  iif "br-tenants" oifname "eth0" accept

  # spec §11 denylist
  tcp dport { 25, 465, 587 } drop
  ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 100.64.0.0/10 } drop
  ip6 daddr { fe80::/10, fc00::/7, ff00::/8, ::1/128, ::/128 } drop
}
```

Per-netns nft argv (the new portion, fenced by `ip6 faas`):

```
ip netns exec fc-<inst> nft add table ip6 faas
ip netns exec fc-<inst> nft add chain ip6 faas forward { type filter hook forward priority filter ; policy accept ; }
ip netns exec fc-<inst> nft add rule ip6 faas forward ct state established,related accept
ip netns exec fc-<inst> nft add rule ip6 faas forward iifname tap0 ip6 daddr { fe80::/10, fc00::/7, ff00::/8, ::1/128, ::/128 } drop
```

## Cross-reference

- `pkg/oci/egress.go::deniedCIDRv6` — user-space enforcement. Keep in
  lockstep with `pkg/netns.DefaultHostPolicy.ForwardDenyIPv6CIDRs`.
- `pkg/netns/policy_test.go::TestHostPolicyRenderDeniesIPv6LinkLocalAndULA` —
  table-driven regression on the host render.
- `pkg/netns/config_test.go::TestNftCommandsEnforceEgressPolicy` —
  adds the per-netns `ip6 daddr { … } drop` line to the `wants` list.

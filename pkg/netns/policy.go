// Package netns — host-side firewall rules for tenant egress.
//
// Source of truth for /etc/nftables.conf on the host. The Go render in this
// file is what `make egress-render` writes into the checked-in artifact at
// `deploy/ansible/roles/nftables/files/policy_nftables.conf`; ansible copies
// that artifact onto the host at `make bootstrap` time.
//
// Why Go rather than an ansible `content:` blob: the rendered text is the
// security contract (spec §11 + CLAUDE.md ship-blocker). It needs a regression
// net — `go test ./pkg/netns` runs on every dev box, CI runner, and Lima
// metal guest — and Go-side rendering lets us assert against the literal
// `ip daddr 10.0.0.0/8 drop` lines, not eyeball a YAML string.
//
// Spec §11 says: "Tenant egress: deny 25/465/587, deny RFC1918 + link-local +
// metadata ranges." This file owns the deny-lists. Forward chains see traffic
// from the per-instance netns bridged via `BridgeName`; input chains see
// public traffic to the host. Both honor spec §11.
//
// The bridge-name constant MUST match `TenantBridge` in config.go. The
// TestTenantBridgeMatches test in config_test.go fails CI if anyone drifts.
package netns

import (
	"fmt"
	"strconv"
	"strings"
)

// HostPolicy is the parameter set for rendering the host nftables ruleset.
// Fields map 1:1 to spec §11 + §7 concepts. The export is on this type (not a
// package-level constructor) so tests can vary individual fields and assert
// the substitution behavior.
type HostPolicy struct {
	// BridgeName is the root-ns bridge that all per-instance veth host-sides
	// enslave to (set up by pkg/fcvm/manager.go via the TenantBridge constant
	// in this package). MUST equal `TenantBridge`.
	BridgeName string

	// PublicIface is the host's outward-facing NIC. Spec §7 deployment uses
	// eth0 on the EX44; on the Lima guest it's the NAT'd default route.
	PublicIface string

	// ForwardDenyCIDRs is the egress address denylist (spec §11): RFC1918,
	// link-local (169.254/16 — covers cloud metadata too), CGN (100.64/10).
	ForwardDenyCIDRs []string

	// ForwardDenyIPv6CIDRs is the IPv6 sibling of ForwardDenyCIDRs (spec §11,
	// ADR-023). Spec §11 says "deny RFC1918 + link-local + metadata ranges" —
	// the IPv6 equivalents (fe80::/10 link-local, fc00::/7 ULA, ff00::/8
	// multicast, ::1/128 loopback, ::/128 unspecified) were not in the v1
	// implementation. See ADR-023 for the family-keyword form (`ip6 daddr`)
	// vs `meta nfproto` decision. The list mirrors pkg/oci/egress.go's
	// `deniedCIDRv6` so user-space and firewall enforcement stay in lockstep;
	// keep both lists identical when editing.
	ForwardDenyIPv6CIDRs []string

	// ForwardDenyTCPPorts is the egress TCP port denylist (spec §11):
	// SMTP on 25, 465, 587. Spam = Hetzner abuse desk = existential.
	ForwardDenyTCPPorts []int

	// InputAllowTCPPorts is the inbound TCP allowlist (spec §11 input chain):
	// 22 (sshd ops), 80 (CertMagic HTTP-01 for Pro), 443 (HTTPS). Everything
	// else on the public IFace is dropped by the input chain's `policy drop`.
	InputAllowTCPPorts []int

	// MasqueradeCIDR is the source-address set the postrouting nat chain
	// MASQUERADEs to the host's public IP on its way out PublicIface. Must be
	// the NETWORK form of HostBridgeCIDR (e.g. "10.100.0.0/16", not the host
	// IP ".1" form) — every bridged tenant VM's source falls in this range
	// because pkg/fcvm/alloc.go hands out 10.100.0.2+, never .1 (the
	// allocator reserves slot 0 for the bridge itself). Without this rule
	// the per-netns SNAT translates the guest source to 10.100.x.y, but no
	// root-ns rule rewrites that to the public IP — the public internet
	// has no route back to 10.100.x.y, so every bidirectional flow (TCP /
	// HTTPS / DNS replies) dies at the first SYN-ACK or A-record.
	// Tier-1 of the network roadmap.
	MasqueradeCIDR string
}

// DefaultHostPolicy is the platform-wide host nftables policy. Source of
// truth for the deny-lists per spec §11. Do not inline these values
// anywhere — every consumer of the host ruleset goes through HostPolicy and
// this var.
var DefaultHostPolicy = HostPolicy{
	BridgeName:  TenantBridge, // br-tenants (config.go) — single source of truth.
	PublicIface: "eth0",

	ForwardDenyCIDRs: []string{
		"10.0.0.0/8",     // RFC1918
		"172.16.0.0/12",  // RFC1918
		"192.168.0.0/16", // RFC1918
		"169.254.0.0/16", // link-local + cloud metadata (169.254.169.254 is AWS/GCP metadata IP)
		"100.64.0.0/10",  // CGN (RFC6598)
	},

	// IPv6 deny — mirrors pkg/oci/egress.go::deniedCIDRv6 (see ADR-023).
	// fe80::/10 = link-local (exposes neighbor table to guests).
	// fc00::/7  = ULA (RFC4193; lateral movement into the control plane).
	// ff00::/8  = multicast (no use case in this model).
	// ::1/128   = loopback.
	// ::/128    = unspecified (a packet with no source — misconfigured or
	//             malicious, never routable).
	ForwardDenyIPv6CIDRs: []string{
		"fe80::/10",
		"fc00::/7",
		"ff00::/8",
		"::1/128",
		"::/128",
	},

	ForwardDenyTCPPorts: []int{25, 465, 587},

	InputAllowTCPPorts: []int{22, 80, 443},

	// Tenant source CIDR the postrouting nat chain MASQUERADEs. Network
	// form of HostBridgeCIDR — every bridged tenant VM's host-side IP
	// (10.100.x.y, x.y ≥ 0.2) falls in this range; the bridge IP (.1) is
	// ruled out by the allocator in pkg/fcvm/alloc.go, so this CIDR
	// exactly matches "tenant-originated, not the host" once routed out
	// PublicIface. See HostPolicy.MasqueradeCIDR doc for why this exists.
	MasqueradeCIDR: "10.100.0.0/16",
}

// Render produces the full /etc/nftables.conf body, including the shebang
// line (so the file is exec'd directly by `nft -f`) and a `flush ruleset`
// to clear any prior rules before loading ours.
//
// The shape is intentionally close to the existing ansible-side ruleset: a
// single `table inet faas` with three chains (input, forward, output). The
// input and forward chains both default-drop; the output chain accept-only
// (the host itself reaches anywhere; egress policy is FOR the tenant VMs, not
// for vmmd's own outbound).
//
// Order matters in `forward`: the §11 denylist MUST come BEFORE the
// `iif BridgeName oifname PublicIface accept` allow, otherwise bridged
// tenant traffic matches the broad allow on its first rule and never
// reaches the SMTP / RFC1918 / IPv6 drops (nftables is first-match).
// The per-netns chain (`pkg/netns/config.go::NftCommands`) is the primary
// block at the guest-originated layer; this host-side ordering is
// defense-in-depth so a misconfigured or bypassed netns chain still
// fails closed at the host layer. `ct state established,related accept`
// stays first so replies on published connections survive — a reply on
// a published connection's daddr ∈ 10.100.0.0/16 ⊂ 10.0.0.0/8 would
// otherwise hit the new RFC1918 drop. v4 deny must stay directly above
// v6 deny — see ADR-023.
func (h HostPolicy) Render() string {
	if h.BridgeName == "" || h.PublicIface == "" || h.MasqueradeCIDR == "" {
		// Hard fail rather than render a broken ruleset — a forward chain
		// without iif/oif or an input chain with no allowlist would silently
		// drop everything, and a postrouting nat chain with no source CIDR
		// would MASQUERADE every outbound packet (including vmmd's own) to
		// the tenant bridge range, which would be a security regression.
		panic("netns: HostPolicy.Render: BridgeName, PublicIface, and MasqueradeCIDR are required")
	}

	denyCIDRs := strings.Join(h.ForwardDenyCIDRs, " ")
	denyIPv6CIDRs := strings.Join(h.ForwardDenyIPv6CIDRs, " ")
	denyPorts := joinInts(h.ForwardDenyTCPPorts, ",")
	allowPorts := joinInts(h.InputAllowTCPPorts, ",")

	var b strings.Builder
	b.WriteString("#!/usr/sbin/nft -f\n")
	b.WriteString("# onebox-faas nftables.conf (spec §7, §11)\n")
	b.WriteString("# Tenant egress denylist — SMTP, RFC1918, link-local, metadata.\n")
	b.WriteString("# Tap proxy NAT — used by gatewayd for outbound guest traffic.\n")
	b.WriteString("\n")
	b.WriteString("flush ruleset\n")
	b.WriteString("\n")
	b.WriteString("table inet faas {\n")
	b.WriteString("  chain input {\n")
	b.WriteString("    type filter hook input priority 0; policy drop;\n")
	b.WriteString("    ct state established,related accept\n")
	b.WriteString("    iif lo accept\n")
	fmt.Fprintf(&b, "    iifname %q accept\n", h.BridgeName)
	fmt.Fprintf(&b, "    tcp dport { %s } accept     # sshd + gatewayd public listener\n", allowPorts)
	b.WriteString("  }\n")
	b.WriteString("\n")
	b.WriteString("  chain forward {\n")
	b.WriteString("    type filter hook forward priority 0; policy drop;\n")
	b.WriteString("    ct state established,related accept\n")
	b.WriteString("\n")
	b.WriteString("    # spec §11 denylist — evaluated BEFORE the bridged-tenant broad allow\n")
	b.WriteString("    # so tenant traffic to RFC1918 / SMTP / link-local is actually dropped at\n")
	b.WriteString("    # the host layer; the per-netns chain is the primary block, this is defense\n")
	b.WriteString("    # in depth.\n")
	fmt.Fprintf(&b, "    tcp dport { %s } drop\n", denyPorts)
	fmt.Fprintf(&b, "    ip daddr { %s } drop\n", denyCIDRs)
	fmt.Fprintf(&b, "    ip6 daddr { %s } drop\n", denyIPv6CIDRs)
	fmt.Fprintf(&b, "    iifname %q oifname %q accept\n", h.BridgeName, h.PublicIface)
	b.WriteString("  }\n")
	b.WriteString("\n")
	b.WriteString("  chain output {\n")
	b.WriteString("    type filter hook output priority 0; policy accept;\n")
	b.WriteString("  }\n")
	b.WriteString("\n")
	b.WriteString("  chain postrouting {\n")
	b.WriteString("    type nat hook postrouting priority srcnat; policy accept;\n")
	fmt.Fprintf(&b, "    ip saddr %s oifname %q masquerade\n", h.MasqueradeCIDR, h.PublicIface)
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

// joinInts renders a port slice as comma-joined digits: "25,465,587". The
// nftables tcp-dport set syntax is `{ 25,465,587 } drop`.
func joinInts(in []int, sep string) string {
	parts := make([]string, len(in))
	for i, n := range in {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, sep)
}

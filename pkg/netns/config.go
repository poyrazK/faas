package netns

import (
	"fmt"
	"net/netip"
)

// Inner-world constants (ADR-009, spec §7). Every guest sees the IDENTICAL
// network: guest 10.0.0.2/30 behind tap0, gateway 10.0.0.1. That sameness is
// exactly what lets one snapshot restore as N concurrent instances — the
// per-instance uniqueness lives entirely on the host side (veth + host IP),
// never inside the guest. Do not make these per-VM.
const (
	GuestIP        = "10.0.0.2"
	GuestGateway   = "10.0.0.1"
	GuestPrefix    = "10.0.0.2/30"
	TapPrefix      = "10.0.0.1/30" // host (tap0) side of the /30 inside the netns
	AppPort        = 8080          // the :8080 contract (spec §2)
	TenantBridge   = "br-tenants"  // root-ns bridge the veth host-side enslaves to
	HostBridgeCIDR = "10.100.0.1/16"
)

// Config is the per-instance network plan. All uniqueness (Netns name, veth
// names, HostIP) comes from the fcvm allocator; this package only turns it into
// the exact command sequence to realise and tear down the topology:
//
//	root ns:  br-tenants ── vethHost ─┐
//	                                  │  (veth pair)
//	netns fc-<instance>:  vethPeer(10.100.x.y/16) ── [route/DNAT] ── tap0(10.0.0.1) ── guest(10.0.0.2)
type Config struct {
	Instance   string     // instance id
	Netns      string     // fc-<instance>
	Tap        string     // tap0 (identical in every netns)
	VethHost   string     // root-ns end, enslaved to br-tenants
	VethPeer   string     // netns end, holds HostIP
	HostIP     netip.Addr // routable identity, 10.100.x.y
	HostBits   int        // prefix length for HostIP (16)
	EgressMbit int        // per-plan egress cap via tc on VethHost; 0 = no cap (legacy / disabled)
}

// NewConfig fills the constant fields (tap name, /16) around the allocated names
// and IP.
func NewConfig(instance, netnsName, vethHost, vethPeer string, hostIP netip.Addr) Config {
	return Config{
		Instance: instance,
		Netns:    netnsName,
		Tap:      "tap0",
		VethHost: vethHost,
		VethPeer: vethPeer,
		HostIP:   hostIP,
		HostBits: 16,
	}
}

// hostCIDR renders HostIP with its prefix, e.g. "10.100.0.2/16".
func (c Config) hostCIDR() string {
	return fmt.Sprintf("%s/%d", c.HostIP, c.HostBits)
}

// SetupCommands returns the ordered argv list that creates the namespace, veth
// pair, tap device, and addressing. Each element is a full command (no shell).
// The metal layer executes them in order; a failure at step N must trigger
// Teardown so nothing leaks (invariant §6.2-4/5, `make leakcheck`).
func (c Config) SetupCommands() [][]string {
	nx := []string{"ip", "netns", "exec", c.Netns} // prefix for in-netns commands
	cmd := func(parts ...string) []string { return parts }
	inNetns := func(parts ...string) []string { return append(append([]string{}, nx...), parts...) }

	return [][]string{
		// Namespace + loopback.
		cmd("ip", "netns", "add", c.Netns),
		inNetns("ip", "link", "set", "lo", "up"),
		// veth pair; host end onto the tenant bridge, peer end into the netns.
		cmd("ip", "link", "add", c.VethHost, "type", "veth", "peer", "name", c.VethPeer),
		cmd("ip", "link", "set", c.VethHost, "master", TenantBridge),
		cmd("ip", "link", "set", c.VethHost, "up"),
		cmd("ip", "link", "set", c.VethPeer, "netns", c.Netns),
		inNetns("ip", "addr", "add", c.hostCIDR(), "dev", c.VethPeer),
		inNetns("ip", "link", "set", c.VethPeer, "up"),
		// tap0 for firecracker; host side of the guest /30.
		inNetns("ip", "tuntap", "add", c.Tap, "mode", "tap"),
		inNetns("ip", "addr", "add", TapPrefix, "dev", c.Tap),
		inNetns("ip", "link", "set", c.Tap, "up"),
		// Route guest traffic; enable forwarding inside the netns only.
		inNetns("sysctl", "-w", "net.ipv4.ip_forward=1"),
	}
}

// TeardownCommands returns the argv list to remove everything Setup created.
// Deleting the netns takes the peer veth and tap with it; the host-side veth is
// removed explicitly (its deletion is idempotent-safe — errors are ignored by
// the caller). Order matters: delete links before the namespace.
func (c Config) TeardownCommands() [][]string {
	return [][]string{
		{"ip", "link", "del", c.VethHost},
		{"ip", "netns", "del", c.Netns},
	}
}

// NftCommands returns the per-instance nftables ruleset (spec §7) as a sequence
// of argv commands applied inside the netns. It both publishes the instance and
// enforces the ship-blocking tenant egress policy (§11). Two responsibilities:
//
//   - Publish + NAT: traffic arriving on the uplink (VethPeer) to the host
//     identity's :8080 is DNAT'd to the guest's :8080 contract; guest egress is
//     masqueraded behind the host identity.
//   - Egress filter: matched on iifname tap0 so it only touches guest-ORIGINATED
//     traffic and can never break the inbound DNAT path. Deny SMTP (25/465/587 —
//     spam is a Hetzner-abuse existential risk) and deny RFC1918 + link-local
//     (169.254.0.0/16 covers the 169.254.169.254 metadata IP) so a tenant cannot
//     move laterally into the control plane. Default policy is accept (§7
//     default-allow 80/443/53).
//
// Rules live in the netns, so TeardownCommands' `netns del` drops them — no
// explicit nft teardown is needed. Each command is a full argv (nft joins its
// argv and parses it), so this works with the same stdin-less Runner that
// executes SetupCommands. Egress bandwidth (per-plan tc) is applied by
// Config.TcCommands, called by Manager.setupNetwork between SetupCommands
// and NftResetCommands — same argv shape, runs in the root ns since tc
// operates on VethHost. Per-instance conntrack cap (spec §7) is not yet
// wired — separate follow-up.
//
// The returned slice is FATAL — every argv must exit 0. For idempotency-reset
// commands (delete/flush, which exit non-zero when the table is absent), see
// NftResetCommands; those run best-effort ahead of the ruleset so a Wake that
// re-enters an existing netns doesn't fail on `delete table`.
func (c Config) NftCommands() [][]string {
	nx := []string{"ip", "netns", "exec", c.Netns, "nft"}
	nft := func(parts ...string) []string { return append(append([]string{}, nx...), parts...) }
	port := fmt.Sprintf("%d", AppPort)
	return [][]string{
		nft("add", "table", "ip", "faas"),
		// NAT: publish :8080 to the guest; masquerade the guest's egress.
		nft("add", "chain", "ip", "faas", "prerouting", "{", "type", "nat", "hook", "prerouting", "priority", "dstnat", ";", "}"),
		nft("add", "rule", "ip", "faas", "prerouting", "iifname", c.VethPeer, "tcp", "dport", port, "dnat", "to", fmt.Sprintf("%s:%d", GuestIP, AppPort)),
		nft("add", "chain", "ip", "faas", "postrouting", "{", "type", "nat", "hook", "postrouting", "priority", "srcnat", ";", "}"),
		nft("add", "rule", "ip", "faas", "postrouting", "oifname", c.VethPeer, "masquerade"),
		// Egress filter (§11): default-accept, deny from the guest side only.
		nft("add", "chain", "ip", "faas", "forward", "{", "type", "filter", "hook", "forward", "priority", "filter", ";", "policy", "accept", ";", "}"),
		// Accept reply traffic first. The inbound DNAT'd request is published from
		// the host identity (10.100.x.y ∈ 10.100.0.0/16), so the guest's reply
		// leaves iifname tap0 with daddr in that range — which is ALSO inside the
		// 10.0.0.0/8 lateral-movement deny below. Without this established/related
		// accept the deny would drop every reply and no published request would
		// ever complete. Guest-INITIATED (ct state new) traffic still falls through
		// to the denies, so lateral movement stays blocked.
		nft("add", "rule", "ip", "faas", "forward", "ct", "state", "established,related", "accept"),
		nft("add", "rule", "ip", "faas", "forward", "iifname", c.Tap, "tcp", "dport", "{", "25,", "465,", "587", "}", "drop"),
		// CGN (100.64.0.0/10) included for symmetry with pkg/netns.DefaultHostPolicy
		// .ForwardDenyCIDRs — see #32 for IPv6 follow-up.
		nft("add", "rule", "ip", "faas", "forward", "iifname", c.Tap, "ip", "daddr", "{", "10.0.0.0/8,", "172.16.0.0/12,", "192.168.0.0/16,", "169.254.0.0/16,", "100.64.0.0/10", "}", "drop"),
	}
}

// NftResetCommands returns the best-effort argv list that brings the
// per-netns nftables table to a clean slate before NftCommands runs. The
// single `delete table` exits non-zero on a fresh netns (no table to
// delete) — that is expected; the caller logs the failure and continues.
// On a snapshot-restore Wake the table exists (the netns outlived the VM),
// so the delete succeeds and the subsequent `add table` in NftCommands
// does not collide.
//
// Best-effort, not fatal — this is the only place in the per-instance
// lifecycle where we accept nft returning non-zero. Splitting it from
// NftCommands keeps the ruleset's own commands strictly atomic.
func (c Config) NftResetCommands() [][]string {
	nx := []string{"ip", "netns", "exec", c.Netns, "nft"}
	nft := func(parts ...string) []string { return append(append([]string{}, nx...), parts...) }
	return [][]string{
		nft("delete", "table", "ip", "faas"),
	}
}

// TcCommands returns the argv list that applies the per-plan egress
// bandwidth cap on the host-side veth (spec §7: per-instance egress cap
// 10 / 25 / 100 / 250 Mbit via tc). VethHost is the host-side end of the
// pair (root-ns, enslaved to br-tenants); the qdisc lives there so it
// caps bytes regardless of which direction the kernel attributes them.
// No ip netns exec prefix — tc runs in the root namespace.
//
// tbf is the simplest single-rate shaper; an htb class hierarchy is
// unnecessary for a per-veth root cap where every instance gets its own
// qdisc on its own link. burst 32kbit / latency 400ms are conservative
// defaults — burst covers ~4× the smallest rate's packet-per-MTU
// comfortably inside tbf's limit ceiling.
//
// When EgressMbit == 0 the caller MUST skip TcCommands entirely (see
// Manager.setupNetwork's `if nc.EgressMbit > 0` guard). Returning an
// empty slice here would silently swallow a misconfigured cap; refusing
// to run the argv when zero is the clearer behaviour.
func (c Config) TcCommands() [][]string {
	return [][]string{
		{"tc", "qdisc", "add", "dev", c.VethHost, "root", "tbf",
			"rate", fmt.Sprintf("%dmbit", c.EgressMbit),
			"burst", "32kbit", "latency", "400ms"},
	}
}

// TcResetCommands returns the best-effort argv list that removes any
// existing root qdisc on VethHost before TcCommands runs. On a fresh
// boot VethHost is brand-new and `tc qdisc del` exits non-zero (no
// qdisc to delete); the caller logs and continues. On a snapshot-
// restore Wake the netns — and VethHost — outlive the VM (the qdisc
// was applied on the prior wake and the link was kept up across
// park), so `tc qdisc del` succeeds and lets the subsequent `add`
// win. Mirrors the NftResetCommands pattern (PR #36).
//
// No teardown is needed: ip link del VethHost in TeardownCommands
// drops the qdisc when it drops the link.
func (c Config) TcResetCommands() [][]string {
	return [][]string{
		{"tc", "qdisc", "del", "dev", c.VethHost, "root"},
	}
}

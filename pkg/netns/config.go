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
	Instance string     // instance id
	Netns    string     // fc-<instance>
	Tap      string     // tap0 (identical in every netns)
	VethHost string     // root-ns end, enslaved to br-tenants
	VethPeer string     // netns end, holds HostIP
	HostIP   netip.Addr // routable identity, 10.100.x.y
	HostBits int        // prefix length for HostIP (16)
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
// executes SetupCommands. Egress bandwidth (per-plan tc) and the per-instance
// conntrack cap (§7) are applied elsewhere, not here.
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
		nft("add", "rule", "ip", "faas", "forward", "iifname", c.Tap, "ip", "daddr", "{", "10.0.0.0/8,", "172.16.0.0/12,", "192.168.0.0/16,", "169.254.0.0/16", "}", "drop"),
	}
}

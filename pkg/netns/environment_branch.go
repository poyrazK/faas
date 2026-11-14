// adr: 20261031193635
package netns

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Contained environment branches (ADR-20261031193635 section 7, slice 6a).
// The branch's guests reach the platform services of their branch and nothing
// else: published ingress and its replies, the bridge's canonical service
// proxy (which resolves a branch caller's targets inside the branch), the
// bridge resolver, and the endpoints of the managed databases bound in the
// branch. Every other guest-initiated packet is dropped, IPv6 included.

const (
	// EnvironmentBranchTable is the separate inet table of the contained
	// profile. Ordinary firewall resets and egress updates never touch it.
	EnvironmentBranchTable = "gregale_branch"
	// EnvironmentBranchDatabaseSet holds the address . port pairs of the
	// branch's own managed databases, added by vmmd when the guest resolves
	// one of their names.
	EnvironmentBranchDatabaseSet = "branch_db"
)

// EnvironmentBranchNftCommands installs the contained profile's forward hook
// at priority -10, before the ordinary chains. Its drop is final; its accepts
// only let a packet go on to the ordinary policy, so the profile can only
// narrow what the ordinary rules allow. The table and its set are created
// once and the chain is flushed and rebuilt in the same nft transaction, so a
// re-render keeps the set's elements and never leaves the hook open.
func (c Config) EnvironmentBranchNftCommands() [][]string {
	if !c.EnvironmentBranchContained {
		return nil
	}
	var out [][]string
	add := func(args ...string) {
		out = append(out, append([]string{"ip", "netns", "exec", c.Netns, "nft"}, args...))
	}
	add("add", "table", "inet", EnvironmentBranchTable)
	add("add", "set", "inet", EnvironmentBranchTable, EnvironmentBranchDatabaseSet,
		"{", "type", "ipv4_addr", ".", "inet_service", ";", "flags", "timeout", ";", "}")
	add("add", "chain", "inet", EnvironmentBranchTable, "forward",
		"{", "type", "filter", "hook", "forward", "priority", "-10", ";", "policy", "drop", ";", "}")
	add("flush", "chain", "inet", EnvironmentBranchTable, "forward")
	rule := func(args ...string) {
		add(append([]string{"add", "rule", "inet", EnvironmentBranchTable, "forward"}, args...)...)
	}
	// Only replies to connections initiated outside the guest are exempt; a
	// generic established accept would keep connections opened before the
	// profile was installed.
	rule("ct", "direction", "reply", "ct", "state", "established,related", "accept")
	rule("iifname", c.VethPeer, "oifname", c.Tap, "ip", "daddr", GuestIP,
		"tcp", "dport", strconv.Itoa(c.guestAppPort()), "ct", "status", "dnat", "accept")
	if c.HostBridgeIP.IsValid() {
		bridge := c.HostBridgeIP.String()
		rule("iifname", c.Tap, "ip", "saddr", GuestIP, "ip", "daddr", bridge, "tcp", "dport", strconv.Itoa(ServiceProxyPort), "accept")
		// Slice 6c: the bridge object relay, which forwards only requests
		// signed with this branch's own object storage keys.
		rule("iifname", c.Tap, "ip", "saddr", GuestIP, "ip", "daddr", bridge, "tcp", "dport", strconv.Itoa(api.EnvironmentBranchObjectRelayPort), "accept")
		for _, protocol := range []string{"tcp", "udp"} {
			rule("iifname", c.Tap, "ip", "saddr", GuestIP, "ip", "daddr", bridge, protocol, "dport", strconv.Itoa(ServiceDiscoveryDNSPort), "accept")
		}
	}
	rule("iifname", c.Tap, "ip", "saddr", GuestIP, "ip", "daddr", ".", "tcp", "dport", "@"+EnvironmentBranchDatabaseSet, "accept")
	return out
}

// EnvironmentBranchDatabaseAddCommands opens the IPv4 addresses of a branch
// database name on its port for ttl. IPv6 answers are not added: the profile
// has no IPv6 exception.
func (c Config) EnvironmentBranchDatabaseAddCommands(addrs []netip.Addr, port uint16, ttl time.Duration) [][]string {
	if !c.EnvironmentBranchContained || port == 0 {
		return nil
	}
	secs := int(max(ttl, time.Second) / time.Second)
	var elems []string
	for _, a := range addrs {
		if a = a.Unmap(); a.Is4() {
			elems = append(elems, fmt.Sprintf("%s . %d timeout %ds", a, port, secs))
		}
	}
	if len(elems) == 0 {
		return nil
	}
	return [][]string{{"ip", "netns", "exec", c.Netns, "nft", "add", "element", "inet", EnvironmentBranchTable,
		EnvironmentBranchDatabaseSet, "{", strings.Join(elems, ", "), "}"}}
}

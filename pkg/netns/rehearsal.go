// adr: 1349
package netns

import (
	"strconv"

	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
)

// RehearsalServiceDiscoveryDNSPort is a separate private-only bridge listener.
// Guest requests to the ordinary resolver are transparently redirected here;
// classification never depends on a not-yet-published runtime identity.
const RehearsalServiceDiscoveryDNSPort = 5354

const rehearsalPostgresProxyPort = rehearsalpg.ProxyPort
const rehearsalObjectProxyPort = rehearsalobj.ProxyPort

// RehearsalNftCommands installs a separate forward hook. Ordinary live egress,
// DNS-gate, private-network and operator policy updates cannot relax this hook.
// add/flush rebuilds the table in the same nft transaction without deleting it
// between renders. This policy must be installed before releasing guest code.
func (c Config) RehearsalNftCommands() [][]string {
	if c.RehearsalRunID == "" {
		return nil
	}
	var out [][]string
	add := func(args ...string) {
		out = append(out, append([]string{"ip", "netns", "exec", c.Netns, "nft"}, args...))
	}
	add("add", "table", "inet", "gregale_rehearsal")
	add("flush", "table", "inet", "gregale_rehearsal")
	add("add", "chain", "inet", "gregale_rehearsal", "prerouting", "{", "type", "nat", "hook", "prerouting", "priority", "-110", ";", "}")
	if c.HostBridgeIP.IsValid() {
		for _, protocol := range []string{"tcp", "udp"} {
			add("add", "rule", "inet", "gregale_rehearsal", "prerouting", "iifname", c.Tap,
				"ip", "saddr", GuestIP, "ip", "daddr", c.HostBridgeIP.String(), protocol, "dport", strconv.Itoa(ServiceDiscoveryDNSPort),
				"dnat", "ip", "to", c.HostBridgeIP.String()+":"+strconv.Itoa(RehearsalServiceDiscoveryDNSPort))
		}
	}
	add("add", "chain", "inet", "gregale_rehearsal", "forward", "{", "type", "filter", "hook", "forward", "priority", "-10", ";", "policy", "drop", ";", "}")
	rule := func(args ...string) {
		add(append([]string{"add", "rule", "inet", "gregale_rehearsal", "forward"}, args...)...)
	}
	// Only replies to connections initiated outside the guest are exempt.
	// A generic established accept would preserve old outbound connections.
	rule("ct", "direction", "reply", "ct", "state", "established,related", "accept")
	rule("iifname", c.VethPeer, "oifname", c.Tap, "ip", "daddr", GuestIP,
		"tcp", "dport", strconv.Itoa(c.guestAppPort()), "ct", "status", "dnat", "accept")
	if c.HostBridgeIP.IsValid() {
		// The canonical proxy authorizes both identities against the retained
		// run policy. DNS refuses public lookups and returns the bridge address.
		for _, port := range []int{ServiceProxyPort, RehearsalServiceDiscoveryDNSPort, rehearsalpg.ProxyPort, rehearsalobj.ProxyPort} {
			rule("iifname", c.Tap, "ip", "saddr", GuestIP, "ip", "daddr", c.HostBridgeIP.String(), "tcp", "dport", strconv.Itoa(port), "accept")
		}
		rule("iifname", c.Tap, "ip", "saddr", GuestIP, "ip", "daddr", c.HostBridgeIP.String(), "udp", "dport", strconv.Itoa(RehearsalServiceDiscoveryDNSPort), "accept")
	}
	return out
}

// adr: 1643 — node-local public bridges cross the public veth before delivery.
package netns

import "strconv"

// Only the root bridge source can reach arbitrary declared guest ports. Other
// namespaces retain the existing :8080 publication. The main-port swap comes
// before that rule; identity mappings follow it, and private ingress is separate.
func (c Config) publicBridgeDNATRules(nft func(...string) []string, beforeMain bool) [][]string {
	if !c.PublicForwarding || !c.HostBridgeIP.IsValid() || !c.HostBridgeIP.Is4() || !c.HostIP.IsValid() || !c.HostIP.Is4() {
		return nil
	}
	prefix := []string{"add", "rule", "ip", "faas", "prerouting", "iifname", c.VethPeer,
		"ip", "saddr", c.HostBridgeIP.String(), "ip", "daddr", c.HostIP.String()}
	rule := func(args ...string) []string {
		return nft(append(append([]string{}, prefix...), args...)...)
	}
	if beforeMain {
		if c.guestAppPort() == AppPort {
			return nil
		}
		return [][]string{rule("tcp", "dport", strconv.Itoa(c.guestAppPort()), "dnat", "to", GuestIP+":"+strconv.Itoa(AppPort))}
	}
	return [][]string{rule("meta", "l4proto", "{", "tcp,udp", "}", "dnat", "to", GuestIP)}
}

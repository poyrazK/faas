package netns

import (
	"net/netip"
	"strconv"
	"strings"
)

const privatePolicyTable = "faas_private_policy"

// PrivateNetworkHistoryCommands remembers destinations for classification, never
// authorization. Keep this set across policy replacement: conntrack may retain
// a DNAT or route to a withdrawn network, including after vmmd restarts.
func (c Config) PrivateNetworkHistoryCommands(prefixes []netip.Prefix) [][]string {
	values := privateIPv4Prefixes(prefixes)
	if len(values) == 0 {
		return nil
	}
	return [][]string{{"ip", "netns", "exec", c.Netns, "nft", "add", "element", "ip", privatePolicyTable, "destinations", "{", strings.Join(values, ","), "}"}}
}

// Recheck the original tuple in BOTH directions before the regular forward
// chain's established/related accept. A return still subjects the flow to all
// other base chains. A drop revokes an old conntrack authorization immediately.
func (c Config) privateConntrackPolicyCommands(nft func(...string) []string) [][]string {
	var cmds [][]string
	add := func(args ...string) { cmds = append(cmds, nft(args...)) }
	add("add", "table", "ip", privatePolicyTable)
	add("add", "set", "ip", privatePolicyTable, "destinations", "{", "type", "ipv4_addr", ";", "flags", "interval", ";", "auto-merge", ";", "}")
	cmds = append(cmds, c.PrivateNetworkHistoryCommands(c.PrivateNetworkCIDRs)...)
	add("add", "chain", "ip", privatePolicyTable, "forward", "{", "type", "filter", "hook", "forward", "priority", "-5", ";", "policy", "accept", ";", "}")
	add("add", "chain", "ip", privatePolicyTable, "egress")
	add("add", "chain", "ip", privatePolicyTable, "ingress")
	for _, chain := range []string{"forward", "egress", "ingress"} {
		add("flush", "chain", "ip", privatePolicyTable, chain)
	}
	rule := func(chain string, args ...string) {
		add(append([]string{"add", "rule", "ip", privatePolicyTable, chain}, args...)...)
	}
	// Platform routes can overlap a customer CIDR on operator-configured
	// bridges. They retain their existing port/DNS/quarantine gates.
	addresses := []netip.Addr{c.HostIP, c.HostBridgeIP}
	if !c.QualificationOnly {
		addresses = append(addresses, c.DatabaseRouteAddress)
	}
	for _, address := range addresses {
		if address.IsValid() && address.Is4() {
			rule("forward", "ct", "original", "ip", "daddr", address.String(), "return")
		}
	}
	if !c.QualificationOnly && c.ServiceAddressCIDR.IsValid() && c.ServiceAddressCIDR.Addr().Is4() {
		rule("forward", "ct", "original", "ip", "daddr", c.ServiceAddressCIDR.String(), "return")
	}
	operatorExempt := false
	for _, p := range c.OperatorExceptions {
		operatorExempt = operatorExempt || p.Contains(netip.MustParseAddr(GuestIP))
	}
	if !operatorExempt {
		rule("forward", "ct", "original", "ip", "daddr", "@destinations", "ct", "original", "ip", "saddr", GuestIP, "jump", "egress")
	}
	rule("forward", "ct", "original", "ip", "daddr", "@destinations", "ct", "original", "ip", "saddr", "!=", GuestIP, "jump", "ingress")

	// A cached DNAT for a withdrawn VIP must not survive the withdrawal.
	var ingressAddresses []string
	if c.PrivateVethPeer != "" {
		for _, a := range []netip.Addr{c.PrivateNetworkAddress, c.PrivateNetworkServiceAddress} {
			if a.IsValid() && a.Is4() {
				ingressAddresses = append(ingressAddresses, a.String())
			}
		}
	}
	if len(ingressAddresses) > 0 {
		rule("ingress", "ct", "original", "ip", "daddr", "!=", "{", strings.Join(ingressAddresses, ","), "}", "drop")
		rule("ingress", "ct", "status", "!=", "dnat", "drop")
	}

	if len(c.PrivateNetworkCIDRs) > 0 {
		if len(c.PrivateNetworkFirewallRules) == 0 {
			prefixes := c.PrivateNetworkCIDRs
			if len(c.PrivateNetworkAllowedCIDRs) > 0 {
				prefixes = c.PrivateNetworkAllowedCIDRs
			}
			if len(privateIPv4Prefixes(prefixes)) > 0 {
				rule("egress", "ct", "original", "ip", "daddr", "{", strings.Join(privateIPv4Prefixes(prefixes), ","), "}", "return")
			}
			if len(ingressAddresses) > 0 {
				args := []string{"ct", "protocol", "tcp", "ct", "original", "proto-dst", strconv.Itoa(AppPort)}
				if len(c.PrivateNetworkAllowedCIDRs) > 0 {
					args = append(args, "ct", "original", "ip", "saddr", "{", strings.Join(privateIPv4Prefixes(c.PrivateNetworkAllowedCIDRs), ","), "}")
				}
				rule("ingress", append(args, "return")...)
			}
		} else {
			for _, fw := range c.PrivateNetworkFirewallRules {
				prefixes := c.privateFirewallPrefixes(fw)
				if len(privateIPv4Prefixes(prefixes)) == 0 || (fw.Direction == "ingress" && len(ingressAddresses) == 0) {
					continue
				}
				chain, field := "egress", "daddr"
				if fw.Direction == "ingress" {
					chain, field = "ingress", "saddr"
				}
				args := []string{"ct", "original", "ip", field, "{", strings.Join(privateIPv4Prefixes(prefixes), ","), "}", "ct", "protocol", fw.Protocol}
				if fw.Protocol != "icmp" {
					ports := firewallPortTokens(fw.Protocol, fw.Ports)
					if len(ports) < 3 {
						continue
					}
					args = append(args, "ct", "original", "proto-dst")
					args = append(args, ports[2:]...)
				}
				rule(chain, append(args, "return")...)
			}
		}
	}
	rule("egress", "drop")
	rule("ingress", "drop")
	return cmds
}

func privateIPv4Prefixes(prefixes []netip.Prefix) []string {
	var values []string
	for _, p := range prefixes {
		if p.IsValid() && p.Addr().Is4() {
			values = append(values, p.Masked().String())
		}
	}
	return values
}

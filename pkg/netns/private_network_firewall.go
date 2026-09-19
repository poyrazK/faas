package netns

import (
	"net/netip"
	"strconv"
	"strings"
)

// PrivateNetworkFirewallPortRange is an inclusive L4 port range.
type PrivateNetworkFirewallPortRange struct {
	Start uint16
	End   uint16
}

// PrivateNetworkFirewallRule is the renderer-neutral form of an API firewall
// rule. The API validator has already canonicalized direction/protocol and
// checked that every prefix belongs to the private network.
type PrivateNetworkFirewallRule struct {
	Direction string
	Protocol  string
	CIDRs     []netip.Prefix
	Ports     []PrivateNetworkFirewallPortRange
}

func firewallPortTokens(protocol string, ports []PrivateNetworkFirewallPortRange) []string {
	if len(ports) == 0 {
		return nil
	}
	values := make([]string, 0, len(ports))
	for _, port := range ports {
		value := strconv.Itoa(int(port.Start))
		if port.End != port.Start {
			value += "-" + strconv.Itoa(int(port.End))
		}
		values = append(values, value)
	}
	if len(values) == 1 {
		return []string{protocol, "dport", values[0]}
	}
	return []string{protocol, "dport", "{", strings.Join(values, ","), "}"}
}

func (c Config) privateFirewallPrefixes(rule PrivateNetworkFirewallRule) []netip.Prefix {
	base := c.PrivateNetworkAllowedCIDRs
	if len(rule.CIDRs) == 0 {
		if len(base) > 0 {
			return base
		}
		return c.PrivateNetworkCIDRs
	}
	if len(base) == 0 {
		return rule.CIDRs
	}
	// A rule can narrow the reusable CIDR baseline but never broaden it. For
	// overlapping CIDRs, the more-specific prefix is the exact intersection.
	out := make([]netip.Prefix, 0, len(rule.CIDRs))
	for _, candidate := range rule.CIDRs {
		for _, allowed := range base {
			if allowed.Contains(candidate.Addr()) && allowed.Bits() <= candidate.Bits() {
				out = append(out, candidate)
			} else if candidate.Contains(allowed.Addr()) && candidate.Bits() <= allowed.Bits() {
				out = append(out, allowed)
			}
		}
	}
	return out
}

// ForwardPrivateNetworkFirewallRules emits the per-rule v4 egress accepts.
// The caller places these before Gregale's RFC1918 deny set.
func (c Config) ForwardPrivateNetworkFirewallRules(nft func(...string) []string) [][]string {
	if len(c.PrivateNetworkFirewallRules) == 0 {
		return nil
	}
	out := make([][]string, 0, len(c.PrivateNetworkFirewallRules))
	for _, rule := range c.PrivateNetworkFirewallRules {
		if rule.Direction != "egress" {
			continue
		}
		var prefixes []string
		for _, prefix := range c.privateFirewallPrefixes(rule) {
			if prefix.IsValid() && prefix.Addr().Is4() {
				prefixes = append(prefixes, prefix.String())
			}
		}
		if len(prefixes) == 0 {
			continue
		}
		args := []string{"add", "rule", "ip", "faas", "forward", "iifname", c.Tap, "ip", "daddr", "{", strings.Join(prefixes, ","), "}"}
		if rule.Protocol == "icmp" {
			args = append(args, "ip", "protocol", "icmp")
		} else {
			args = append(args, firewallPortTokens(rule.Protocol, rule.Ports)...)
		}
		args = append(args, "accept")
		out = append(out, nft(args...))
	}
	return out
}

// ForwardPrivateNetworkFirewallRules6 is reserved for a future dual-stack
// network contract; the current API validator accepts IPv4 only.
func (c Config) ForwardPrivateNetworkFirewallRules6(nft func(...string) []string) [][]string {
	return nil
}

func (c Config) privateFirewallIngressRules(nft func(...string) []string) [][]string {
	if len(c.PrivateNetworkFirewallRules) == 0 || c.PrivateVethPeer == "" {
		return nil
	}
	out := make([][]string, 0, len(c.PrivateNetworkFirewallRules))
	for _, rule := range c.PrivateNetworkFirewallRules {
		if rule.Direction != "ingress" {
			continue
		}
		var prefixes []string
		for _, prefix := range c.privateFirewallPrefixes(rule) {
			if prefix.IsValid() && prefix.Addr().Is4() {
				prefixes = append(prefixes, prefix.String())
			}
		}
		if len(prefixes) == 0 {
			continue
		}
		args := []string{"add", "rule", "ip", "faas", "forward", "iifname", c.PrivateVethPeer, "ip", "saddr", "{", strings.Join(prefixes, ","), "}", "ip", "daddr", GuestIP}
		if rule.Protocol == "icmp" {
			args = append(args, "ip", "protocol", "icmp")
		} else {
			args = append(args, firewallPortTokens(rule.Protocol, rule.Ports)...)
		}
		args = append(args, "accept")
		out = append(out, nft(args...))
	}
	return out
}

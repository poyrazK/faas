// adr: 1642 — enforcement precedes the billable root veth RX boundary.
package netns

import (
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

const EgressBudgetTable = "faas_budget"

func (c Config) egressBudgetRules(bytes int64) [][]string {
	prefix := []string{"ip", "netns", "exec", c.Netns, "nft"}
	nft := func(args ...string) []string { return append(append([]string{}, prefix...), args...) }
	return [][]string{
		nft("add", "rule", "netdev", EgressBudgetTable, "egress", "jump", "lifetime"),
		nft("add", "rule", "netdev", EgressBudgetTable, "egress", "meta", "length", "<", strconv.Itoa(api.FinancialEgressMinimumPacketBytes), "drop"),
		nft("add", "rule", "netdev", EgressBudgetTable, "egress", "meta", "protocol", "!=", "{", "ip,ip6,arp", "}", "drop"),
		nft("add", "rule", "netdev", EgressBudgetTable, "egress", "quota", strconv.FormatInt(bytes/api.FinancialEgressFramingFactor, 10), "bytes", "counter", "accept"),
	}
}
func (c Config) EgressBudgetSetupCommands() [][]string {
	if c.EgressBudgetBytes <= 0 {
		return nil
	}
	prefix := []string{"ip", "netns", "exec", c.Netns, "nft"}
	nft := func(args ...string) []string { return append(append([]string{}, prefix...), args...) }
	cmds := [][]string{
		nft("add", "table", "netdev", EgressBudgetTable),
		nft("add", "chain", "netdev", EgressBudgetTable, "egress", "{", "type", "filter", "hook", "egress", "device", c.VethPeer, "priority", "0", ";", "policy", "drop", ";", "}"),
	}
	cmds = append(cmds, nft("add", "chain", "netdev", EgressBudgetTable, "lifetime"))
	cmds = append(cmds, c.EgressBudgetDeadlineCommands()...)
	return append(cmds, c.egressBudgetRules(c.EgressBudgetBytes)...)
}

// Only the native attempt lock may call this, once per newly funded sequence.
// A failed/unknown application must never retry a fresh anonymous quota.
func (c Config) EgressBudgetRefillCommands(bytes int64) [][]string {
	if bytes <= 0 {
		return nil
	}
	return append([][]string{{"ip", "netns", "exec", c.Netns, "nft", "flush", "chain", "netdev", EgressBudgetTable, "egress"}}, c.egressBudgetRules(bytes)...)
}

// Kernel wall-clock expiry also bounds failed-kill or dead-daemon traffic to
// its funded UTC window. Nodes require synchronized UTC; vmmd validates skew
// against authority responses and uses the authority timeout as an early pad.
func (c Config) EgressBudgetDeadlineCommands() [][]string {
	prefix := []string{"ip", "netns", "exec", c.Netns, "nft"}
	nft := func(args ...string) []string { return append(append([]string{}, prefix...), args...) }
	return [][]string{
		nft("flush", "chain", "netdev", EgressBudgetTable, "lifetime"),
		nft("add", "rule", "netdev", EgressBudgetTable, "lifetime", "meta", "time", ">=", strconv.FormatInt(c.EgressBudgetUntil.Unix(), 10), "drop"),
	}
}

// Segment guest GSO/GRO traffic before it can reach the public-peer quota.
// Otherwise one small GSO aggregate could expand into many billed Ethernet
// frames AFTER quota accounting. The IFB redirects back into TAP ingress after
// transmit validation, with segmentation/aggregation offloads disabled.
func (c Config) EgressBudgetSegmentationCommands() [][]string {
	if c.EgressBudgetBytes <= 0 {
		return nil
	}
	prefix := []string{"ip", "netns", "exec", c.Netns}
	in := func(args ...string) []string { return append(append([]string{}, prefix...), args...) }
	return [][]string{
		in("ip", "link", "add", "fb-egress", "type", "ifb"),
		in("ethtool", "-K", "fb-egress", "sg", "off", "tso", "off", "gso", "off", "gro", "off", "tx-udp-segmentation", "off", "tx-gso-list", "off"),
		in("ip", "link", "set", "fb-egress", "up"),
		in("tc", "qdisc", "add", "dev", c.Tap, "handle", "ffff:", "ingress"),
		in("tc", "filter", "add", "dev", c.Tap, "parent", "ffff:", "protocol", "all", "matchall", "action", "mirred", "egress", "redirect", "dev", "fb-egress"),
	}
}

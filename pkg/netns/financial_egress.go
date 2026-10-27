// adr: 1748 — enforcement precedes the billable root veth RX boundary.
package netns

import (
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

const EgressBudgetTable = "faas_budget"

// EgressBudgetQuotas splits financed interface bytes between the two kernel
// gates (ADR-1748 Amendment 1). The TAP's segmentation IFB re-injects every
// guest packet with the netdev egress hook disabled for it, so guest IP is
// counted at the namespace's inet postrouting hook, in network bytes that
// framing at most doubles. The peer's netdev egress chain still sees what the
// namespace sends itself (ARP, ND, ICMP) and counts those whole frames. Root
// veth RX therefore stays within local + 2*ip <= bytes. The local share equals
// the refill low-water mark, so ARP alone cannot run out before a refill.
func EgressBudgetQuotas(bytes int64) (local, ip int64) {
	local = bytes / 4
	return local, (bytes - local) / api.FinancialEgressFramingFactor
}

func (c Config) egressBudgetRules(bytes int64) [][]string {
	prefix := []string{"ip", "netns", "exec", c.Netns, "nft"}
	nft := func(args ...string) []string { return append(append([]string{}, prefix...), args...) }
	local, ip := EgressBudgetQuotas(bytes)
	return [][]string{
		nft("add", "rule", "netdev", EgressBudgetTable, "egress", "jump", "lifetime"),
		nft("add", "rule", "netdev", EgressBudgetTable, "egress", "meta", "length", "<", strconv.Itoa(api.FinancialEgressMinimumPacketBytes), "drop"),
		nft("add", "rule", "netdev", EgressBudgetTable, "egress", "meta", "protocol", "!=", "{", "ip,ip6,arp", "}", "drop"),
		nft("add", "rule", "netdev", EgressBudgetTable, "egress", "quota", strconv.FormatInt(local, 10), "bytes", "counter", "accept"),
		nft("add", "rule", "inet", EgressBudgetTable, "gate", "jump", "lifetime"),
		nft("add", "rule", "inet", EgressBudgetTable, "gate", "quota", strconv.FormatInt(ip, 10), "bytes", "counter", "accept"),
		nft("add", "rule", "inet", EgressBudgetTable, "gate", "drop"),
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
		nft("add", "chain", "netdev", EgressBudgetTable, "lifetime"),
		nft("add", "table", "inet", EgressBudgetTable),
		nft("add", "chain", "inet", EgressBudgetTable, "lifetime"),
		nft("add", "chain", "inet", EgressBudgetTable, "gate"),
		nft("add", "chain", "inet", EgressBudgetTable, "postrouting", "{", "type", "filter", "hook", "postrouting", "priority", "0", ";", "policy", "accept", ";", "}"),
	}
	cmds = append(cmds, c.EgressBudgetDeadlineCommands()...)
	cmds = append(cmds, c.egressBudgetRules(c.EgressBudgetBytes)...)
	// Only the public peer is metered; the private side link stays outside.
	return append(cmds, nft("add", "rule", "inet", EgressBudgetTable, "postrouting", "oifname", c.VethPeer, "jump", "gate"))
}

// Only the native attempt lock may call this, once per newly funded sequence.
// A failed/unknown application must never retry a fresh anonymous quota.
func (c Config) EgressBudgetRefillCommands(bytes int64) [][]string {
	if bytes <= 0 {
		return nil
	}
	prefix := []string{"ip", "netns", "exec", c.Netns, "nft"}
	return append([][]string{
		append(append([]string{}, prefix...), "flush", "chain", "netdev", EgressBudgetTable, "egress"),
		append(append([]string{}, prefix...), "flush", "chain", "inet", EgressBudgetTable, "gate"),
	}, c.egressBudgetRules(bytes)...)
}

// Kernel wall-clock expiry also bounds failed-kill or dead-daemon traffic to
// its funded UTC window. Nodes require synchronized UTC; vmmd validates skew
// against authority responses and uses the authority timeout as an early pad.
func (c Config) EgressBudgetDeadlineCommands() [][]string {
	prefix := []string{"ip", "netns", "exec", c.Netns, "nft"}
	nft := func(args ...string) []string { return append(append([]string{}, prefix...), args...) }
	until := strconv.FormatInt(c.EgressBudgetUntil.Unix(), 10)
	return [][]string{
		nft("flush", "chain", "netdev", EgressBudgetTable, "lifetime"),
		nft("add", "rule", "netdev", EgressBudgetTable, "lifetime", "meta", "time", ">=", until, "drop"),
		nft("flush", "chain", "inet", EgressBudgetTable, "lifetime"),
		nft("add", "rule", "inet", EgressBudgetTable, "lifetime", "meta", "time", ">=", until, "drop"),
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

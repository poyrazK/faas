package netns

import "strings"

// EgressInternalTxCounter counts the guest's own traffic to Gregale-internal
// destinations (billing review R8-2, ADR-20261114100000). Billed app egress is
// the root-side veth counter (ADR-046), which counts every byte the guest
// sends; meterd subtracts this counter when internal egress is excluded from
// billing. It is a byte counter, unlike the packet-only deny counters.
const EgressInternalTxCounter = "faas_internal_tx"

// internalEgressChain is where the accounting rule runs: a filter chain on
// postrouting after every forward decision, so a packet the egress filter
// dropped is never counted, and only packets that actually leave toward the
// veth (the ones the billed counter sees) are.
const internalEgressChain = "egress_account"

// InternalEgressDestinations are the destinations a guest-originated packet
// counts as internal for: the private ranges behind which Gregale serves its
// own endpoints (the host bridge with the service proxy, DNS and relays) and
// any provider-verified private destination. Every other destination is the
// internet. IPv6 guest egress has no internal destinations today.
var InternalEgressDestinations = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10"}

// EthernetHeaderBytes is the framing the veth byte counter includes and an
// IP-layer nftables byte counter does not; internal bytes are reported with
// it added per packet so they subtract from the veth counter like for like.
const EthernetHeaderBytes = 14

// internalEgressAccounting returns the counter object, chain and rule that
// count guest-originated packets (conntrack original direction) leaving
// toward the veth for an internal destination. Replies to connections the
// guest accepted (gateway and service-proxy requests) are not counted: an
// app's responses to its users are egress.
func (c Config) internalEgressAccounting(nft func(...string) []string) [][]string {
	if c.VethPeer == "" || c.QualificationOnly {
		return nil
	}
	return [][]string{
		nft("add", "counter", "ip", "faas", EgressInternalTxCounter, "{}"),
		nft("add", "chain", "ip", "faas", internalEgressChain, "{", "type", "filter", "hook", "postrouting", "priority", "300", ";", "policy", "accept", ";", "}"),
		nft("add", "rule", "ip", "faas", internalEgressChain, "oifname", c.VethPeer, "ct", "direction", "original",
			"ip", "daddr", "{", strings.Join(InternalEgressDestinations, ","), "}", "counter", "name", EgressInternalTxCounter),
	}
}

// InternalEgressBytes is the counter's bytes in veth terms (IP bytes plus
// Ethernet framing per packet), read from a parsed counter set.
func InternalEgressBytes(counters map[string]uint64) uint64 {
	return counters[EgressInternalTxCounter+CounterBytesSuffix] + EthernetHeaderBytes*counters[EgressInternalTxCounter]
}

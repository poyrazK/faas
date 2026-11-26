package netns

import (
	"net/netip"
	"sort"
	"strings"
	"sync"
)

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
// own endpoints (the host bridge with the service proxy, DNS and relays), any
// provider-verified private destination, and the platform addresses in the
// RFC 2544 range: private TCP service addresses (198.19.0.0/16, ADR-576) and
// the managed-database route (198.18.0.1, ADR-1027). The host, not the netns,
// translates those two, so in this chain the packet still carries the
// platform address; leaving the range out billed every app-to-app service call
// and native database session as internet egress (metering review
// 2026-11-23). Every other destination is the internet unless the operator
// adds it with SetInternalEgressExtraDestinations. IPv6 guest egress has no
// internal destinations today.
var InternalEgressDestinations = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "198.18.0.0/15"}

var (
	internalEgressExtraMu sync.RWMutex
	internalEgressExtra   []netip.Prefix
)

// SetInternalEgressExtraDestinations is vmmd's setter for the public
// destinations that are still Gregale's own (ADR-20261114100000 amendment
// 2026-11-23): the nodes' public entry addresses, which an app reaches when
// it calls another app, object storage or telemetry by public hostname, and
// the managed PostgreSQL provider's endpoints, whose traffic the database's
// own egress allowance covers. Every namespace prepared afterwards counts
// them as internal. IPv6 and invalid prefixes are ignored.
func SetInternalEgressExtraDestinations(prefixes []netip.Prefix) {
	out := make([]netip.Prefix, 0, len(prefixes))
	for _, p := range prefixes {
		if p.IsValid() && p.Addr().Is4() {
			out = append(out, p.Masked())
		}
	}
	internalEgressExtraMu.Lock()
	internalEgressExtra = out
	internalEgressExtraMu.Unlock()
}

// internalEgressDestinationList is the built-in ranges plus the operator's
// extra destinations, without overlaps: an anonymous nftables interval set
// rejects overlapping elements, so a destination already inside a listed
// range is dropped and a wider one replaces the narrower ones it covers.
func internalEgressDestinationList() []string {
	internalEgressExtraMu.RLock()
	extra := append([]netip.Prefix(nil), internalEgressExtra...)
	internalEgressExtraMu.RUnlock()
	all := make([]netip.Prefix, 0, len(InternalEgressDestinations)+len(extra))
	for _, s := range InternalEgressDestinations {
		all = append(all, netip.MustParsePrefix(s))
	}
	all = append(all, extra...)
	// Widest first, so a covering prefix is kept before what it covers.
	sort.SliceStable(all, func(i, j int) bool { return all[i].Bits() < all[j].Bits() })
	kept := make([]netip.Prefix, 0, len(all))
	for _, p := range all {
		covered := false
		for _, k := range kept {
			if k.Overlaps(p) {
				covered = true
				break
			}
		}
		if !covered {
			kept = append(kept, p)
		}
	}
	sort.Slice(kept, func(i, j int) bool {
		if c := kept[i].Addr().Compare(kept[j].Addr()); c != 0 {
			return c < 0
		}
		return kept[i].Bits() < kept[j].Bits()
	})
	out := make([]string, len(kept))
	for i, p := range kept {
		out[i] = p.String()
	}
	return out
}

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
			"ip", "daddr", "{", strings.Join(internalEgressDestinationList(), ","), "}", "counter", "name", EgressInternalTxCounter),
	}
}

// InternalEgressBytes is the counter's bytes in veth terms (IP bytes plus
// Ethernet framing per packet), read from a parsed counter set.
func InternalEgressBytes(counters map[string]uint64) uint64 {
	return counters[EgressInternalTxCounter+CounterBytesSuffix] + EthernetHeaderBytes*counters[EgressInternalTxCounter]
}

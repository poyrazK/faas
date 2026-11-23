package netns

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
)

// ADR-20261123100000: a guest's 443 call to another compute edge's entry
// address is DNATed to this node's own IPv4 entry address, so it is served by
// the local edge (which sees the guest's own bridge address) instead of
// leaving the node masqueraded to its primary address.
func TestHostPolicyPeerEntryHairpin(t *testing.T) {
	p := DefaultHostPolicy
	p.PublicEntryAddrs = []netip.Addr{netip.MustParseAddr("2001:db8::10"), netip.MustParseAddr("203.0.113.10")}
	p.PeerEntryAddrs = []netip.Addr{netip.MustParseAddr("203.0.113.11"), netip.MustParseAddr("203.0.113.12")}
	out := p.Render()
	want := fmt.Sprintf("  chain prerouting {\n    type nat hook prerouting priority dstnat; policy accept;\n    iifname %q ip daddr { 203.0.113.11, 203.0.113.12 } tcp dport 443 dnat ip to 203.0.113.10\n  }\n", p.BridgeName)
	if !strings.Contains(out, want) {
		t.Fatalf("hairpin prerouting chain missing:\n%s", out)
	}
	// The DNATed call lands on the input chain's bridge accept, before the
	// entry address's per-source backstop.
	accept := strings.Index(out, fmt.Sprintf("iifname %q tcp dport { ", p.BridgeName))
	backstop := strings.Index(out, "ip daddr 203.0.113.10 tcp dport 443 ct state new add")
	if accept < 0 || backstop < 0 || accept > backstop {
		t.Fatalf("bridge hairpin accept must precede the entry backstop:\n%s", out)
	}

	// No IPv4 entry address of its own: nothing to hairpin to.
	p.PublicEntryAddrs = []netip.Addr{netip.MustParseAddr("2001:db8::10")}
	if out := p.Render(); strings.Contains(out, "chain prerouting") {
		t.Fatalf("hairpin rendered without an IPv4 entry address:\n%s", out)
	}
}

func TestHostPolicyPeerEntryAddrsRejectsOwnOrIPv6(t *testing.T) {
	for name, peer := range map[string]string{"own": "203.0.113.10", "ipv6": "2001:db8::11"} {
		t.Run(name, func(t *testing.T) {
			p := DefaultHostPolicy
			p.PublicEntryAddrs = []netip.Addr{netip.MustParseAddr("203.0.113.10")}
			p.PeerEntryAddrs = []netip.Addr{netip.MustParseAddr(peer)}
			defer func() {
				if recover() == nil {
					t.Fatalf("Render accepted peer entry address %s", peer)
				}
			}()
			p.Render()
		})
	}
}

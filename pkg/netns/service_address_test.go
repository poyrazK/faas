// adr: 568
package netns

import (
	"net/netip"
	"strings"
	"testing"
)

var testServiceAddressCIDR = netip.MustParsePrefix("198.19.0.0/16")

func serviceAddressTestConfig() Config {
	c := egressTestConfig()
	c.DNSGated = true
	c.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")}
	c.ConntrackCap = 1024
	c.ServiceAddressCIDR = testServiceAddressCIDR
	return c
}

// Without the switch, no guest may reach the block: the rendered rules are
// exactly the pre-ADR-568 set.
func TestServiceAddressAdmissionIsOptIn(t *testing.T) {
	c := serviceAddressTestConfig()
	c.ServiceAddressCIDR = netip.Prefix{}
	for _, line := range renderedLines(c) {
		if strings.Contains(line, "198.19.") {
			t.Fatalf("disabled config rendered %q", line)
		}
	}
	ipv6 := serviceAddressTestConfig()
	ipv6.ServiceAddressCIDR = netip.MustParsePrefix("fd00::/64")
	for _, line := range renderedLines(ipv6) {
		if strings.Contains(line, "fd00::") {
			t.Fatalf("an IPv6 service prefix rendered %q; service addresses are IPv4 only", line)
		}
	}
}

// Internal TCP is a platform hop: it must be admitted after the conntrack
// cap but ahead of every ADR-361/373 tenant egress rule, otherwise a Free
// app (80/443 only) could never reach a service on 5432, and DNS gating
// would drop it because service addresses are never resolved upstream.
func TestServiceAddressAdmissionPrecedesTenantEgressPolicy(t *testing.T) {
	lines := renderedLines(serviceAddressTestConfig())
	accept := lineIndex(t, lines, "rule ip faas forward iifname tap0 ip daddr 198.19.0.0/16 meta l4proto tcp accept")
	drop := lineIndex(t, lines, "rule ip faas forward iifname tap0 ip daddr 198.19.0.0/16 drop")
	if accept >= drop {
		t.Fatalf("service TCP accept (%d) must precede the service-block drop (%d)", accept, drop)
	}
	if capIdx := lineIndex(t, lines, "ct count over 1024"); capIdx >= accept {
		t.Fatalf("conntrack cap (%d) must bound service connections (accept at %d)", capIdx, accept)
	}
	for _, later := range []string{
		"add @" + EgressDstSet,                                 // ADR-361 fan-out counter
		"ip faas forward iifname tap0 ip daddr { 1.2.3.0/24 }", // ADR-031 allowlist
		"daddr != @egress_resolved",                            // ADR-373 DNS gate
		"tcp dport != @egress_ports",                           // ADR-361 port policy
	} {
		if idx := lineIndex(t, lines, later); drop >= idx {
			t.Fatalf("service-block rules (%d) must precede %q (%d)", drop, later, idx)
		}
	}
}

// A namespace prepared before the switch flipped must not satisfy a request
// made after it: Config equality is the ADR-149 reuse gate.
func TestNewConfigCarriesDefaultServiceAddressCIDR(t *testing.T) {
	prev := DefaultServiceAddressCIDR
	t.Cleanup(func() { SetDefaultServiceAddressCIDR(prev) })

	SetDefaultServiceAddressCIDR(netip.Prefix{})
	before := NewConfig("i", "fc-i", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))
	SetDefaultServiceAddressCIDR(testServiceAddressCIDR)
	after := NewConfig("i", "fc-i", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))
	if after.ServiceAddressCIDR != testServiceAddressCIDR {
		t.Fatalf("NewConfig ServiceAddressCIDR = %s, want %s", after.ServiceAddressCIDR, testServiceAddressCIDR)
	}
	if before.ServiceAddressCIDR.IsValid() {
		t.Fatalf("NewConfig with the switch off carried %s", before.ServiceAddressCIDR)
	}
}

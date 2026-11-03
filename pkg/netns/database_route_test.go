// adr: 1027
package netns

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

var testDatabaseRouteAddress = netip.MustParseAddr("198.18.0.1")

func databaseRouteTestConfig() Config {
	c := serviceAddressTestConfig()
	c.DatabaseRouteAddress = testDatabaseRouteAddress
	return c
}

func TestDatabaseRouteConstantsAgree(t *testing.T) {
	if DatabaseRoutePort != api.DatabaseRoutePort || testDatabaseRouteAddress != api.DatabaseRouteAddress() {
		t.Fatalf("netns and api disagree on the database route: %d/%s vs %d/%s",
			DatabaseRoutePort, testDatabaseRouteAddress, api.DatabaseRoutePort, api.DatabaseRouteAddress())
	}
	if api.ServiceAddressCIDR().Contains(api.DatabaseRouteAddress()) {
		t.Fatal("the database route address must sit outside the ADR-576 service block")
	}
}

// Without the switch no guest may reach the address.
func TestDatabaseRouteAdmissionIsOptIn(t *testing.T) {
	c := databaseRouteTestConfig()
	c.DatabaseRouteAddress = netip.Addr{}
	for _, line := range renderedLines(c) {
		if strings.Contains(line, "198.18.") {
			t.Fatalf("disabled config rendered %q", line)
		}
	}
	qualification := databaseRouteTestConfig()
	qualification.QualificationOnly = true
	for _, line := range renderedLines(qualification) {
		if strings.Contains(line, "198.18.") {
			t.Fatalf("a qualification-only namespace rendered %q", line)
		}
	}
}

// The route is a platform hop: admitted after the conntrack cap and ahead of
// every tenant egress rule, so a Free app (80/443 only, DNS-gated) reaches
// its database without declaring 5432. Only the one port is admitted.
func TestDatabaseRouteAdmissionPrecedesTenantEgressPolicy(t *testing.T) {
	lines := renderedLines(databaseRouteTestConfig())
	accept := lineIndex(t, lines, "rule ip faas forward iifname tap0 ip daddr 198.18.0.1 tcp dport 5432 accept")
	drop := lineIndex(t, lines, "rule ip faas forward iifname tap0 ip daddr 198.18.0.1 drop")
	if accept >= drop {
		t.Fatalf("database route accept (%d) must precede its drop (%d)", accept, drop)
	}
	if capIdx := lineIndex(t, lines, "ct count over 1024"); capIdx >= accept {
		t.Fatalf("conntrack cap (%d) must bound database sessions (accept at %d)", capIdx, accept)
	}
	for _, later := range []string{
		"add @" + EgressDstSet,
		"ip faas forward iifname tap0 ip daddr { 1.2.3.0/24 }",
		"daddr != @egress_resolved",
		"tcp dport != @egress_ports",
	} {
		if idx := lineIndex(t, lines, later); drop >= idx {
			t.Fatalf("database route rules (%d) must precede %q (%d)", drop, later, idx)
		}
	}
}

// A namespace prepared before the switch flipped must not satisfy a request
// made after it: Config equality is the ADR-149 reuse gate.
func TestNewConfigCarriesDefaultDatabaseRouteAddress(t *testing.T) {
	prev := DefaultDatabaseRouteAddress
	t.Cleanup(func() { SetDefaultDatabaseRouteAddress(prev) })

	SetDefaultDatabaseRouteAddress(netip.Addr{})
	before := NewConfig("i", "fc-i", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))
	SetDefaultDatabaseRouteAddress(testDatabaseRouteAddress)
	after := NewConfig("i", "fc-i", "vh0", "vp0", netip.MustParseAddr("10.100.0.2"))
	if after.DatabaseRouteAddress != testDatabaseRouteAddress || before.DatabaseRouteAddress.IsValid() {
		t.Fatalf("NewConfig DatabaseRouteAddress = %s (on) / %s (off)", after.DatabaseRouteAddress, before.DatabaseRouteAddress)
	}
}

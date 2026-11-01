// adr: 1027
package netns

import (
	"net/netip"
	"strings"
	"testing"
)

func databaseRouteHostPolicy(withServiceTCP bool) HostPolicy {
	h := DefaultHostPolicy
	if withServiceTCP {
		h.ServiceTCP = NewServiceTCPHostPolicy(testServiceAddressCIDR, netip.MustParseAddr("10.100.0.1"), 10082, false)
	}
	h.DatabaseRoute = NewDatabaseRouteHostPolicy(testDatabaseRouteAddress, netip.MustParseAddr("10.100.0.1"), 10083)
	return h
}

func TestHostPolicyDatabaseRouteIsOptIn(t *testing.T) {
	out := DefaultHostPolicy.Render()
	for _, fragment := range []string{"198.18.", "10083"} {
		if strings.Contains(out, fragment) {
			t.Fatalf("default host policy rendered %q", fragment)
		}
	}
}

func TestHostPolicyDatabaseRouteRender(t *testing.T) {
	dnat := `    iifname "br-tenants" ip daddr 198.18.0.1 tcp dport 5432 dnat ip to 10.100.0.1:10083` + "\n"
	for _, withService := range []bool{false, true} {
		out := databaseRouteHostPolicy(withService).Render()
		chainStart := strings.Index(out, "  chain prerouting {\n    type nat hook prerouting priority dstnat; policy accept;\n")
		rule := strings.Index(out, dnat)
		if chainStart < 0 || rule < chainStart || strings.Count(out, "chain prerouting") != 1 {
			t.Fatalf("service TCP %v: the database DNAT is not in the one prerouting chain:\n%s", withService, out)
		}
		if withService && strings.Index(out, "dnat ip to 10.100.0.1:10082") > rule {
			t.Fatal("the service rules must keep their place ahead of the database rule")
		}
		drop := strings.Index(out, "    ip daddr 198.18.0.1 drop")
		broad := strings.Index(out, `    iifname "br-tenants" oifname "eth0" accept`)
		if drop < 0 || broad < 0 || drop > broad {
			t.Fatalf("database route forward drop (%d) must precede the bridged-tenant accept (%d)", drop, broad)
		}
	}
}

func TestHostPolicyDatabaseRouteRejectsUnsafePolicies(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HostPolicy)
	}{
		{"address inside the tenant bridge", func(h *HostPolicy) { h.DatabaseRoute.Address = netip.MustParseAddr("10.100.9.9") }},
		{"address inside an overlay", func(h *HostPolicy) { h.OverlayCIDRs = []string{"198.18.0.0/24"} }},
		{"address inside the service block", func(h *HostPolicy) { h.DatabaseRoute.Address = netip.MustParseAddr("198.19.0.7") }},
		{"IPv6 address", func(h *HostPolicy) { h.DatabaseRoute.Address = netip.MustParseAddr("fd00::1") }},
		{"bridge outside the masquerade CIDR", func(h *HostPolicy) { h.DatabaseRoute.BridgeIP = netip.MustParseAddr("10.101.0.1") }},
		{"proxy port is the client port", func(h *HostPolicy) { h.DatabaseRoute.ProxyPort = 5432 }},
		{"proxy port is the service TCP proxy", func(h *HostPolicy) { h.DatabaseRoute.ProxyPort = 10082 }},
		{"proxy port is an HTTP service port", func(h *HostPolicy) { h.DatabaseRoute.ProxyPort = 10081 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := databaseRouteHostPolicy(true)
			tt.mutate(&h)
			defer func() {
				if recover() == nil {
					t.Fatal("Render accepted an unsafe database route policy")
				}
			}()
			_ = h.Render()
		})
	}
}

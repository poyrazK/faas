// adr: 568
package netns

import (
	"net/netip"
	"strings"
	"testing"
)

func serviceTCPHostPolicy(httpsEnabled bool) HostPolicy {
	h := DefaultHostPolicy
	h.ServiceTCP = NewServiceTCPHostPolicy(testServiceAddressCIDR, netip.MustParseAddr("10.100.0.1"), 10082, httpsEnabled)
	return h
}

func TestHostPolicyServiceTCPIsOptIn(t *testing.T) {
	out := DefaultHostPolicy.Render()
	for _, fragment := range []string{"prerouting", "198.19.", "10082"} {
		if strings.Contains(out, fragment) {
			t.Fatalf("default host policy rendered %q", fragment)
		}
	}
}

func TestHostPolicyServiceTCPRender(t *testing.T) {
	tests := []struct {
		name      string
		https     bool
		httpRule  string
		forbidden string
	}{
		{
			name:      "without private HTTPS",
			httpRule:  `    iifname "br-tenants" ip daddr 198.19.0.0/16 tcp dport { 10080, 10081 } dnat ip to 10.100.0.1` + "\n",
			forbidden: "443, ",
		},
		{
			name:     "with private HTTPS",
			https:    true,
			httpRule: `    iifname "br-tenants" ip daddr 198.19.0.0/16 tcp dport { 443, 10080, 10081 } dnat ip to 10.100.0.1` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := serviceTCPHostPolicy(tt.https)
			out := h.Render()
			chain := "  chain prerouting {\n" +
				"    type nat hook prerouting priority dstnat; policy accept;\n" +
				tt.httpRule +
				`    iifname "br-tenants" ip daddr 198.19.0.0/16 meta l4proto tcp dnat ip to 10.100.0.1:10082` + "\n" +
				"  }\n"
			if !strings.Contains(out, chain) {
				t.Fatalf("rendered ruleset lacks the service prerouting chain:\n%s", out)
			}
			if tt.forbidden != "" && strings.Contains(out, tt.forbidden) {
				t.Fatalf("rendered %q although private HTTPS is off", tt.forbidden)
			}
			// The chain is nat-only: it must sit after the filter chains and
			// before postrouting, keeping one table layout.
			if strings.Index(out, "  chain output {") > strings.Index(out, "  chain prerouting {") ||
				strings.Index(out, "  chain prerouting {") > strings.Index(out, "  chain postrouting {") {
				t.Fatal("prerouting chain is not between output and postrouting")
			}
			drop := strings.Index(out, "    ip daddr 198.19.0.0/16 drop")
			broad := strings.Index(out, `    iifname "br-tenants" oifname "eth0" accept`)
			if drop < 0 || broad < 0 || drop > broad {
				t.Fatalf("service-block forward drop (%d) must precede the bridged-tenant accept (%d)", drop, broad)
			}
		})
	}
}

func TestHostPolicyServiceTCPRejectsUnsafePolicies(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HostPolicy)
	}{
		{"bridge outside the masquerade CIDR", func(h *HostPolicy) { h.ServiceTCP.BridgeIP = netip.MustParseAddr("10.101.0.1") }},
		{"block overlapping the tenant bridge", func(h *HostPolicy) { h.ServiceTCP.AddressCIDR = netip.MustParsePrefix("10.100.0.0/24") }},
		{"block overlapping an overlay", func(h *HostPolicy) { h.OverlayCIDRs = []string{"198.19.128.0/17"} }},
		{"IPv6 block", func(h *HostPolicy) { h.ServiceTCP.AddressCIDR = netip.MustParsePrefix("fd00::/64") }},
		{"proxy port on the HTTP mesh", func(h *HostPolicy) { h.ServiceTCP.ProxyPort = ServiceProxyPort }},
		{"no HTTP ports", func(h *HostPolicy) { h.ServiceTCP.HTTPPorts = nil }},
		{"invalid proxy port", func(h *HostPolicy) { h.ServiceTCP.ProxyPort = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := serviceTCPHostPolicy(false)
			tt.mutate(&h)
			defer func() {
				if recover() == nil {
					t.Fatal("Render accepted an unsafe service TCP policy")
				}
			}()
			_ = h.Render()
		})
	}
}

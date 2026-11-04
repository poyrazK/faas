package netns

import (
	"net/netip"
	"strings"
	"testing"
)

func TestStaticEgressSourcePrecedesMasqueradeWithAndWithoutGateway(t *testing.T) {
	for _, gateway := range []string{"", "wg-tenant"} {
		t.Run(gateway, func(t *testing.T) {
			policy := DefaultHostPolicy
			policy.TenantEgressIface = gateway
			policy.StaticEgressRules = []StaticEgressRule{{PerVMHostIP: netip.MustParseAddr("10.100.0.2"), CustomerIP: netip.MustParseAddr("203.0.113.42")}}
			rules := policy.Render()
			snat := strings.Index(rules, `ip saddr 10.100.0.2 oifname "eth0" snat to 203.0.113.42`)
			fallback := strings.Index(rules, `ip saddr 10.100.0.0/16 oifname`)
			// The first occurrence may be a filter rule; compare within NAT.
			postrouting := rules[strings.Index(rules, "  chain postrouting {"):]
			if snat < 0 || strings.Index(postrouting, "snat to") > strings.Index(postrouting, "masquerade") || fallback < 0 {
				t.Fatalf("lease SNAT must precede fallback MASQUERADE:\n%s", postrouting)
			}
			if gateway != "" {
				for _, want := range []string{
					`iifname "br-tenants" ip saddr 10.100.0.2 meta mark set meta mark | 0x40000000`,
					`iifname "br-tenants" ip saddr 10.100.0.2 oifname "eth0" accept`,
				} {
					if !strings.Contains(rules, want) {
						t.Errorf("pinned app cannot bypass shared gateway: missing %s", want)
					}
				}
				if strings.Contains(rules, `ip saddr 10.100.0.0/16 meta mark`) {
					t.Fatal("routing exception includes unpinned tenants")
				}
			}
		})
	}
}

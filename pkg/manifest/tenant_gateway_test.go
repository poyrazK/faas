// adr: 372 — tenant egress gateway manifest contract.
package manifest

import (
	"strings"
	"testing"
)

const testGatewayKey = "q0oNLvB3wP4c2zKZ6Vn4l0z1eFQq2iJq5m1nY3Xk5kA="

func TestTenantEgressGatewayValidate(t *testing.T) {
	ok := TenantEgressGateway{Endpoint: "gw.example.net:51820", PublicKey: testGatewayKey, TunnelCIDR: "10.66.0.0/24"}
	if errs := ok.validate("203.0.113.0/24"); len(errs) != 0 {
		t.Fatalf("valid gateway rejected: %v", errs)
	}
	for _, tc := range []struct {
		name, field string
		mutate      func(*TenantEgressGateway)
		overlay     string
	}{
		{"no port", "endpoint", func(g *TenantEgressGateway) { g.Endpoint = "gw.example.net" }, ""},
		{"bad key", "public_key", func(g *TenantEgressGateway) { g.PublicKey = "not-a-key" }, ""},
		{"public tunnel", "tunnel_cidr", func(g *TenantEgressGateway) { g.TunnelCIDR = "203.0.113.0/24" }, ""},
		{"v6 tunnel", "tunnel_cidr", func(g *TenantEgressGateway) { g.TunnelCIDR = "fd00::/64" }, ""},
		{"bridge overlap", "tunnel_cidr", func(g *TenantEgressGateway) { g.TunnelCIDR = "10.100.5.0/24" }, ""},
		{"overlay overlap", "tunnel_cidr", func(*TenantEgressGateway) {}, "10.66.0.0/16"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := ok
			tc.mutate(&g)
			errs := g.validate(tc.overlay)
			if len(errs) == 0 || !strings.Contains(errs.Error(), "egress.tenant_gateway."+tc.field) {
				t.Fatalf("errs = %v, want a %s error", errs, tc.field)
			}
		})
	}
	var absent *TenantEgressGateway
	if errs := absent.validate(""); errs != nil {
		t.Fatalf("absent gateway = %v", errs)
	}
}

func TestTenantTunnelAddress(t *testing.T) {
	for _, tc := range []struct{ cidr, node, want string }{
		{"10.66.0.0/24", "fsn-1", "10.66.0.2/24"},
		{"10.66.0.0/24", "fsn-12", "10.66.0.13/24"},
		{"10.66.0.0/24", "fsn-2.faas", "10.66.0.3/24"},
		{"10.66.4.0/22", "fsn-300", "10.66.5.45/22"},
	} {
		got, err := TenantTunnelAddress(tc.cidr, tc.node)
		if err != nil || got.String() != tc.want {
			t.Errorf("TenantTunnelAddress(%s, %s) = %v, %v; want %s", tc.cidr, tc.node, got, err, tc.want)
		}
	}
	for _, bad := range [][2]string{{"10.66.0.0/29", "fsn-6"}, {"10.66.0.0/24", "gateway"}, {"10.66.0.0/24", "fsn-0"}, {"fd00::/64", "fsn-1"}, {"10.66.0.0/16", "fsn-4294967295"}, {"10.66.0.0/16", "fsn-99999999999999999999"}} {
		if _, err := TenantTunnelAddress(bad[0], bad[1]); err == nil {
			t.Errorf("TenantTunnelAddress(%s, %s) accepted", bad[0], bad[1])
		}
	}
}

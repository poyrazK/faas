// adr: 20261117185346 §1–§2 — the agent egress gateway's manifest contract:
// its own listener, key, tunnel and public addresses, and one tunnel slice
// per compute node.
package manifest

import (
	"strings"
	"testing"
)

const testAgentGatewayKey = "dGhpcyBpcyBhIHRlc3Qga2V5IGZvciB0aGUgYWdlbnQ="

func TestAgentEgressGatewayValidate(t *testing.T) {
	tenant := &TenantEgressGateway{Endpoint: "gw.example.net:51820", PublicKey: testGatewayKey, TunnelCIDR: "10.66.0.0/24"}
	ok := AgentEgressGateway{Endpoint: "gw.example.net:51821", PublicKey: testAgentGatewayKey, TunnelCIDR: "10.78.0.0/16",
		Addresses: []string{"198.51.100.7", "198.51.100.8"}}
	if errs := ok.validate("203.0.113.0/24", tenant); len(errs) != 0 {
		t.Fatalf("valid gateway rejected: %v", errs)
	}
	for _, tc := range []struct {
		name, field string
		mutate      func(*AgentEgressGateway)
		overlay     string
	}{
		{"no port", "endpoint", func(g *AgentEgressGateway) { g.Endpoint = "gw.example.net" }, ""},
		{"tenant listener", "endpoint", func(g *AgentEgressGateway) { g.Endpoint = tenant.Endpoint }, ""},
		{"bad key", "public_key", func(g *AgentEgressGateway) { g.PublicKey = "not-a-key" }, ""},
		{"tenant key", "public_key", func(g *AgentEgressGateway) { g.PublicKey = tenant.PublicKey }, ""},
		{"public tunnel", "tunnel_cidr", func(g *AgentEgressGateway) { g.TunnelCIDR = "203.0.113.0/24" }, ""},
		{"tunnel too small", "tunnel_cidr", func(g *AgentEgressGateway) { g.TunnelCIDR = "10.78.0.0/26" }, ""},
		{"bridge overlap", "tunnel_cidr", func(g *AgentEgressGateway) { g.TunnelCIDR = "10.100.0.0/16" }, ""},
		{"tenant tunnel overlap", "tunnel_cidr", func(g *AgentEgressGateway) { g.TunnelCIDR = "10.66.0.0/16" }, ""},
		{"overlay overlap", "tunnel_cidr", func(*AgentEgressGateway) {}, "10.78.4.0/24"},
		{"one slice", "node_prefix", func(g *AgentEgressGateway) { g.NodePrefix = 16 }, ""},
		{"slice too small", "node_prefix", func(g *AgentEgressGateway) { g.NodePrefix = 30 }, ""},
		{"no addresses", "addresses", func(g *AgentEgressGateway) { g.Addresses = nil }, ""},
		{"private address", "addresses[0]", func(g *AgentEgressGateway) { g.Addresses = []string{"10.1.2.3"} }, ""},
		{"v6 address", "addresses[0]", func(g *AgentEgressGateway) { g.Addresses = []string{"2001:db8::1"} }, ""},
		{"listed twice", "addresses[1]", func(g *AgentEgressGateway) { g.Addresses = []string{"198.51.100.7", "198.51.100.7"} }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := ok
			tc.mutate(&g)
			errs := g.validate(tc.overlay, tenant)
			if len(errs) == 0 || !strings.Contains(errs.Error(), "egress.agent_gateway."+tc.field) {
				t.Fatalf("errs = %v, want a %s error", errs, tc.field)
			}
		})
	}
	var absent *AgentEgressGateway
	if errs := absent.validate("", nil); errs != nil {
		t.Fatalf("absent gateway = %v", errs)
	}
}

func TestAgentNodeSlice(t *testing.T) {
	for _, tc := range []struct {
		cidr        string
		prefix      int
		node        string
		slice, addr string
	}{
		{"10.78.0.0/16", 22, "fsn-1", "10.78.4.0/22", "10.78.4.1"},
		{"10.78.0.0/16", 22, "fsn-3.faas", "10.78.12.0/22", "10.78.12.1"},
		{"10.78.0.0/16", 22, "fsn-63", "10.78.252.0/22", "10.78.252.1"},
		{"10.78.0.0/16", 24, "fsn-2", "10.78.2.0/24", "10.78.2.1"},
	} {
		slice, addr, err := AgentNodeSlice(tc.cidr, tc.prefix, tc.node)
		if err != nil || slice.String() != tc.slice || addr.String() != tc.addr {
			t.Errorf("AgentNodeSlice(%s, /%d, %s) = %v %v, %v; want %s %s", tc.cidr, tc.prefix, tc.node, slice, addr, err, tc.slice, tc.addr)
		}
	}
	for _, bad := range []struct {
		cidr   string
		prefix int
		node   string
	}{
		{"10.78.0.0/16", 22, "fsn-0"},  // slice 0 is the gateway's
		{"10.78.0.0/16", 22, "fsn-64"}, // past the last slice
		{"10.78.0.0/16", 22, "gateway"},
		{"10.78.0.0/16", 16, "fsn-1"},
		{"fd00::/64", 80, "fsn-1"},
	} {
		if _, _, err := AgentNodeSlice(bad.cidr, bad.prefix, bad.node); err == nil {
			t.Errorf("AgentNodeSlice(%s, /%d, %s) accepted", bad.cidr, bad.prefix, bad.node)
		}
	}
	if gw, err := AgentGatewayAddress("10.78.0.0/16"); err != nil || gw.String() != "10.78.0.1/16" {
		t.Errorf("AgentGatewayAddress = %v, %v", gw, err)
	}
}

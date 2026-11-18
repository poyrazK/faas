// adr: 20261117185346 §2–§3 — an agent VM's traffic is marked, leaves only
// through the agent tunnel after every deny, and takes its own address.
package netns

import (
	"net/netip"
	"strings"
	"testing"
)

func TestHostPolicyAgentEgress(t *testing.T) {
	p := DefaultHostPolicy
	p.TenantEgressIface = "wg-tenant"
	p.AgentEgressIface = "wg-agent"
	p.AgentEgressRules = []AgentEgressRule{{PerVMHostIP: netip.MustParseAddr("10.100.0.9"), AgentIP: netip.MustParseAddr("10.78.8.2"),
		AccountID: "acct", SandboxID: "sb"}}
	out := p.Render()
	mark := `iifname "br-tenants" ip saddr 10.100.0.9 meta mark set meta mark | 0x20000000`
	accept := `iifname "br-tenants" ip saddr 10.100.0.9 oifname "wg-agent" accept`
	drop := `iifname "br-tenants" ip saddr 10.100.0.9 drop comment "faas agent egress only via the agent tunnel"`
	snat := `ip saddr 10.100.0.9 oifname "wg-agent" snat to 10.78.8.2`
	for _, want := range []string{mark, accept, drop, snat,
		`iifname "br-tenants" oifname "wg-agent" tcp flags syn tcp option maxseg size set rt mtu`} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q", want)
		}
	}
	at := func(s string) int { return strings.Index(out, s) }
	tenantAccept := `iifname "br-tenants" ip saddr 10.100.0.0/16 oifname "wg-tenant" accept`
	switch {
	case at("\n    tcp dport {") == -1 || at("\n    tcp dport {") > at(accept):
		t.Error("the agent accept must follow the SMTP deny")
	case at(accept) > at(drop) || at(drop) > at(tenantAccept):
		t.Error("the agent accept and drop must precede the tenant accept, accept first")
	case at(snat) > at(`ip saddr 10.100.0.0/16 oifname "wg-tenant" masquerade`):
		t.Error("the agent SNAT must precede the tenant masquerade")
	}
	without := DefaultHostPolicy
	without.TenantEgressIface = "wg-tenant"
	without.AgentEgressIface = "wg-agent"
	if out := without.Render(); strings.Contains(out, "agent_egress_route") || strings.Contains(out, "wg-agent") {
		t.Fatal("no agent VM, yet the render names the agent tunnel")
	}
}

func TestHostPolicyAgentEgressRulesNeedTheInterface(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("rules without an interface rendered")
		}
	}()
	p := DefaultHostPolicy
	p.AgentEgressRules = []AgentEgressRule{{PerVMHostIP: netip.MustParseAddr("10.100.0.9"), AgentIP: netip.MustParseAddr("10.78.8.2")}}
	_ = p.Render()
}

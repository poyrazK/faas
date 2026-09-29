// adr: 372 — tenant egress gateway.
package netns

import (
	"strings"
	"testing"
)

func TestHostPolicyTenantEgressGateway(t *testing.T) {
	p := DefaultHostPolicy
	p.MasqueradeCIDR6 = "fc00::/7"
	p.TenantEgressIface = "wg-tenant"
	out := p.Render()
	for _, want := range []string{
		`iifname "br-tenants" oifname "wg-tenant" tcp flags syn tcp option maxseg size set rt mtu`,
		`iifname "br-tenants" ip saddr 10.100.0.0/16 oifname "wg-tenant" accept`,
		`iifname "br-tenants" ip saddr != 10.100.0.0/16 oifname "eth0" accept`,
		`ip saddr 10.100.0.0/16 oifname "wg-tenant" masquerade`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("gateway render missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`iifname "br-tenants" oifname "eth0" accept`,     // tenant v4 must not reach the public NIC
		`ip saddr 10.100.0.0/16 oifname "eth0" masquerade`, // nor be masqueraded to the node address
		`ip6 saddr fc00::/7`,                               // the IPv4 gateway has no v6 path
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("gateway render still contains %q", forbidden)
		}
	}
	direct := DefaultHostPolicy
	if out := direct.Render(); strings.Contains(out, "maxseg") || !strings.Contains(out, `iifname "br-tenants" oifname "eth0" accept`) {
		t.Fatal("the direct path changed without a tenant egress interface")
	}
}

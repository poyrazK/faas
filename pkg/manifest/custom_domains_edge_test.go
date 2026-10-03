// adr: 520 — self-hosted custom-domain TLS manifest contract.
package manifest

import (
	"strings"
	"testing"
)

func TestCustomDomainsEdgeValidate(t *testing.T) {
	cloudflare := DNS{AppsDomain: "gregale.dev", Mode: "cloudflare"}
	valid := CustomDomainsEdge{Mode: "on_demand", Target: "edge.gregale.dev",
		Addresses: []string{"203.0.113.10", "2001:db8::10"}, ACMEEmail: "ops@gregale.dev"}
	for _, tc := range []struct {
		name    string
		edge    CustomDomainsEdge
		dns     DNS
		wantErr string
	}{
		{name: "disabled", edge: CustomDomainsEdge{}, dns: cloudflare},
		{name: "valid", edge: valid, dns: cloudflare},
		{name: "fields without mode", edge: CustomDomainsEdge{Target: "edge.gregale.dev"}, dns: cloudflare, wantErr: "public_edge.custom_domains.mode"},
		{name: "unknown mode", edge: func() CustomDomainsEdge { e := valid; e.Mode = "dns01"; return e }(), dns: cloudflare, wantErr: "unsupported"},
		{name: "missing target", edge: func() CustomDomainsEdge { e := valid; e.Target = ""; return e }(), dns: cloudflare, wantErr: "public_edge.custom_domains.target"},
		{name: "proxied apex target", edge: func() CustomDomainsEdge { e := valid; e.Target = "gregale.dev"; return e }(), dns: cloudflare, wantErr: "proxied by Cloudflare"},
		{name: "apex target without cloudflare", edge: func() CustomDomainsEdge { e := valid; e.Target = "gregale.dev"; return e }(), dns: DNS{AppsDomain: "gregale.dev", Mode: "manual"}},
		{name: "bad address", edge: func() CustomDomainsEdge { e := valid; e.Addresses = []string{"edge.gregale.dev"}; return e }(), dns: cloudflare, wantErr: "addresses[0]"},
		{name: "missing acme email", edge: func() CustomDomainsEdge { e := valid; e.ACMEEmail = ""; return e }(), dns: cloudflare, wantErr: "acme_email"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.edge.validate(tc.dns)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("validate = %v, want no errors", errs)
				}
				return
			}
			if !strings.Contains(errs.Error(), tc.wantErr) {
				t.Fatalf("validate = %v, want an error mentioning %q", errs, tc.wantErr)
			}
		})
	}
}

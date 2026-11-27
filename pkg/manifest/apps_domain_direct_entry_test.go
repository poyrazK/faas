// adr: 20261112114642
package manifest

import (
	"strings"
	"testing"
)

// Once apps have their own domain, direct entry may name only it: neither
// the certificate every compute node holds nor a DNS-only record may cover
// the platform domain (R8-6). The shared-domain pilot layout is untouched.
func TestDirectEntryStaysOffThePlatformDomain(t *testing.T) {
	moved := DNS{PlatformDomain: "gregale.dev", AppsDomain: "gregale.app", DeployDomain: "gregale.app", AppsDomainAliases: []string{"gregale.dev"}}
	shared := DNS{AppsDomain: "gregale.dev"}
	edge := func(certNames, names, proxied []string) PublicEdge {
		return PublicEdge{ComputeNodes: true, Entry: PublicEdgeEntryBoth, Front: PublicEdgeFrontNone, DNS: &PublicEdgeDNS{
			ZoneID: "zone", Names: names, Proxied: proxied,
			Certificate: &PublicEdgeCertificate{Names: certNames},
		}}
	}
	cases := []struct {
		name    string
		dns     DNS
		edge    PublicEdge
		wantErr []string
	}{
		{name: "the shared pilot layout", dns: shared, edge: edge([]string{"*.gregale.dev"}, []string{"*.gregale.dev"}, nil)},
		{name: "apps domain only", dns: moved, edge: edge([]string{"gregale.app", "*.gregale.app"}, []string{"*.gregale.app"}, nil)},
		{name: "platform wildcard certificate", dns: moved, edge: edge([]string{"*.gregale.app", "*.gregale.dev"}, []string{"*.gregale.app"}, nil),
			wantErr: []string{"public_edge.dns.certificate.names[1]"}},
		{name: "platform host certificate", dns: moved, edge: edge([]string{"*.gregale.app", "API.gregale.dev."}, []string{"*.gregale.app"}, nil),
			wantErr: []string{"public_edge.dns.certificate.names[1]"}},
		{name: "platform apex certificate", dns: moved, edge: edge([]string{"gregale.dev"}, []string{"*.gregale.app"}, nil),
			wantErr: []string{"public_edge.dns.certificate.names[0]"}},
		{name: "dns-only platform record", dns: moved, edge: edge([]string{"*.gregale.app"}, []string{"*.gregale.app", "*.gregale.dev"}, nil),
			wantErr: []string{"public_edge.dns.names[1]"}},
		{name: "proxied platform record", dns: moved, edge: edge([]string{"*.gregale.app"}, []string{"*.gregale.app", "shop.gregale.dev"}, []string{"shop.gregale.dev"})},
		{name: "a domain that only ends like the platform", dns: moved, edge: edge([]string{"*.notgregale.dev"}, []string{"*.gregale.app"}, nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{DNS: tc.dns, PublicEdge: tc.edge}
			errs := m.validateDirectEntryOffPlatformDomain()
			if len(errs) != len(tc.wantErr) {
				t.Fatalf("errors = %v, want fields %v", errs, tc.wantErr)
			}
			for i, field := range tc.wantErr {
				if errs[i].Path != field || !strings.Contains(errs[i].Message, "platform domain gregale.dev") {
					t.Fatalf("error %d = %+v, want %s about the platform domain", i, errs[i], field)
				}
			}
		})
	}
}

// adr: 1343 — app edge on compute nodes manifest contract.
package manifest

import (
	"strings"
	"testing"
)

func TestPublicEdgeComputeNodesValidate(t *testing.T) {
	control := Host{Name: "cp-1", Role: "control-plane"}
	compute := Host{Name: "cn-1", Role: "compute-only"}
	for _, tc := range []struct {
		name    string
		edge    PublicEdge
		hosts   []Host
		wantErr string
	}{
		{name: "off without hosts", edge: PublicEdge{}},
		{name: "on", edge: PublicEdge{ComputeNodes: true}, hosts: []Host{control, compute}},
		{name: "no compute node", edge: PublicEdge{ComputeNodes: true}, hosts: []Host{control}, wantErr: "compute-only host"},
		{name: "no control plane", edge: PublicEdge{ComputeNodes: true}, hosts: []Host{compute}, wantErr: "control-plane host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.edge.validate(tc.hosts)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("validate = %v, want no errors", errs)
				}
				return
			}
			if !strings.Contains(errs.Error(), tc.wantErr) || !strings.Contains(errs.Error(), "public_edge.compute_nodes") {
				t.Fatalf("validate = %v, want a public_edge.compute_nodes error mentioning %q", errs, tc.wantErr)
			}
		})
	}
}

// adr: 20261031105055 — placement routing needs the compute edge it routes.
func TestPublicEdgePlacementRoutingNeedsComputeNodes(t *testing.T) {
	control := Host{Name: "cp-1", Role: "control-plane"}
	compute := Host{Name: "cn-1", Role: "compute-only"}
	if errs := (PublicEdge{ComputeNodes: true, PlacementRouting: true}).validate([]Host{control, compute}); len(errs) != 0 {
		t.Fatalf("validate = %v, want no errors", errs)
	}
	errs := (PublicEdge{PlacementRouting: true}).validate([]Host{control, compute})
	if !strings.Contains(errs.Error(), "public_edge.placement_routing") {
		t.Fatalf("validate = %v, want a public_edge.placement_routing error", errs)
	}
}

// adr: 20261102081618 — public_edge.entry takes tunnel or both, and both
// needs the compute edges whose TLS listener it opens.
func TestPublicEdgeEntry(t *testing.T) {
	fleet := []Host{{Name: "cp-1", Role: "control-plane"}, {Name: "cn-1", Role: "compute-only"}}
	for _, tc := range []struct {
		name       string
		edge       PublicEdge
		wantErr    string
		wantDirect bool
	}{
		{name: "default", edge: PublicEdge{ComputeNodes: true}},
		{name: "tunnel", edge: PublicEdge{ComputeNodes: true, Entry: PublicEdgeEntryTunnel}},
		{name: "both", edge: PublicEdge{ComputeNodes: true, Entry: PublicEdgeEntryBoth}, wantDirect: true},
		{name: "both without compute edges", edge: PublicEdge{Entry: PublicEdgeEntryBoth}, wantErr: "needs public_edge.compute_nodes"},
		{name: "direct is not ready", edge: PublicEdge{ComputeNodes: true, Entry: "direct"}, wantErr: "use both"},
		{name: "unknown", edge: PublicEdge{ComputeNodes: true, Entry: "anycast"}, wantErr: `"anycast" is not tunnel or both`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.edge.validate(fleet)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("validate = %v, want no errors", errs)
				}
			} else if !strings.Contains(errs.Error(), tc.wantErr) {
				t.Fatalf("validate = %v, want %q", errs, tc.wantErr)
			}
			if got := tc.edge.DirectEntry(); got != tc.wantDirect {
				t.Fatalf("DirectEntry = %v, want %v", got, tc.wantDirect)
			}
		})
	}
}

// adr: 20261102081618 — public_edge.dns needs direct entry, a zone, names and
// a compute host with a public IPv4 address.
func TestPublicEdgeDNS(t *testing.T) {
	fleet := []Host{{Name: "cp-1", Role: "control-plane"}, {Name: "cn-1", Role: "compute-only", PublicIPv4: "203.0.113.2"}}
	dns := &PublicEdgeDNS{ZoneID: "zone-1", Names: []string{"pilot.gregale.dev"}}
	direct := PublicEdge{ComputeNodes: true, Entry: PublicEdgeEntryBoth, DNS: dns}
	for _, tc := range []struct {
		name    string
		edge    PublicEdge
		hosts   []Host
		wantErr string
	}{
		{name: "valid", edge: direct, hosts: fleet},
		{name: "on the tunnel", edge: PublicEdge{ComputeNodes: true, DNS: dns}, hosts: fleet, wantErr: "needs public_edge.entry: both"},
		{name: "no zone", edge: PublicEdge{ComputeNodes: true, Entry: PublicEdgeEntryBoth, DNS: &PublicEdgeDNS{Names: dns.Names}}, hosts: fleet, wantErr: "zone_id"},
		{name: "no names", edge: PublicEdge{ComputeNodes: true, Entry: PublicEdgeEntryBoth, DNS: &PublicEdgeDNS{ZoneID: "zone-1"}}, hosts: fleet, wantErr: "public_edge.dns.names"},
		{name: "no addressed compute host", edge: direct, hosts: []Host{fleet[0], {Name: "cn-1", Role: "compute-only"}}, wantErr: "needs a compute-only host with public_ipv4"},
		{name: "not an IPv4 address", edge: direct, hosts: []Host{fleet[0], {Name: "cn-1", Role: "compute-only", PublicIPv4: "2001:db8::1"}}, wantErr: "fleet.hosts[1].public_ipv4"},
		{name: "a certificate", edge: withCertificate(direct, &PublicEdgeCertificate{Names: []string{"*.gregale.dev"}, CA: EdgeCertificateCAProduction, Serve: EdgeCertificateCAStaging}), hosts: fleet},
		{name: "serving an unknown CA", edge: withCertificate(direct, &PublicEdgeCertificate{Names: []string{"*.gregale.dev"}, Serve: "true"}), hosts: fleet, wantErr: "public_edge.dns.certificate.serve"},
		{name: "a certificate without names", edge: withCertificate(direct, &PublicEdgeCertificate{}), hosts: fleet, wantErr: "public_edge.dns.certificate.names"},
		{name: "a certificate naming a host twice", edge: withCertificate(direct, &PublicEdgeCertificate{Names: []string{"*.gregale.dev", "*.Gregale.dev"}}), hosts: fleet, wantErr: "public_edge.dns.certificate.names[1]"},
		{name: "a certificate for a deep wildcard", edge: withCertificate(direct, &PublicEdgeCertificate{Names: []string{"*.*.gregale.dev"}}), hosts: fleet, wantErr: "public_edge.dns.certificate.names[0]"},
		{name: "a certificate from another CA", edge: withCertificate(direct, &PublicEdgeCertificate{Names: []string{"*.gregale.dev"}, CA: "zerossl"}), hosts: fleet, wantErr: "public_edge.dns.certificate.ca"},
		{name: "a transparency watch", edge: withCertificate(direct, &PublicEdgeCertificate{Names: []string{"*.gregale.dev"}, Transparency: &PublicEdgeTransparency{Domain: "gregale.dev", AllowNames: []string{"gregale.dev", "*.gregale.dev"}}}), hosts: fleet},
		{name: "a watch without a domain", edge: withCertificate(direct, &PublicEdgeCertificate{Names: []string{"*.gregale.dev"}, Transparency: &PublicEdgeTransparency{}}), hosts: fleet, wantErr: "public_edge.dns.certificate.transparency.domain"},
		{name: "a watch on a wildcard", edge: withCertificate(direct, &PublicEdgeCertificate{Names: []string{"*.gregale.dev"}, Transparency: &PublicEdgeTransparency{Domain: "*.gregale.dev"}}), hosts: fleet, wantErr: "public_edge.dns.certificate.transparency.domain"},
		{name: "a watch allowing a name twice", edge: withCertificate(direct, &PublicEdgeCertificate{Names: []string{"*.gregale.dev"}, Transparency: &PublicEdgeTransparency{Domain: "gregale.dev", AllowNames: []string{"gregale.dev", "Gregale.dev"}}}), hosts: fleet, wantErr: "public_edge.dns.certificate.transparency.allow_names[1]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.edge.validate(tc.hosts)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("validate = %v, want no errors", errs)
				}
				return
			}
			if !strings.Contains(errs.Error(), tc.wantErr) {
				t.Fatalf("validate = %v, want %q", errs, tc.wantErr)
			}
		})
	}
}

// adr: 20261102081618 — the node set needs placement routing, and local first
// needs the node set, so the edges never settle on one node.
func TestPublicEdgeSpreadSwitches(t *testing.T) {
	fleet := []Host{{Name: "cp-1", Role: "control-plane"}, {Name: "cn-1", Role: "compute-only"}}
	for _, tc := range []struct {
		name    string
		edge    PublicEdge
		wantErr string
	}{
		{name: "both on", edge: PublicEdge{ComputeNodes: true, PlacementRouting: true, PlacementNodeSet: true, LocalFirst: true}},
		{name: "node set first", edge: PublicEdge{ComputeNodes: true, PlacementRouting: true, PlacementNodeSet: true}},
		{name: "node set without routing", edge: PublicEdge{ComputeNodes: true, PlacementNodeSet: true}, wantErr: "public_edge.placement_node_set"},
		{name: "local first without the node set", edge: PublicEdge{ComputeNodes: true, PlacementRouting: true, LocalFirst: true}, wantErr: "public_edge.local_first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.edge.validate(fleet)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("validate = %v, want no errors", errs)
				}
				return
			}
			if !strings.Contains(errs.Error(), tc.wantErr) {
				t.Fatalf("validate = %v, want %q", errs, tc.wantErr)
			}
		})
	}
}

// adr: 20261103163954 — public_edge.front: none needs direct entry and an
// entry address on every compute host, apart from the addresses it is
// managed on; dns.proxied only means something under it.
func TestPublicEdgeFront(t *testing.T) {
	control := Host{Name: "cp-1", Role: "control-plane"}
	entered := Host{Name: "cn-1", Role: "compute-only", Address: "10.42.0.2:50051", PublicIPv4: "203.0.113.2", EntryIPv4: "198.51.100.2"}
	direct := PublicEdge{ComputeNodes: true, Entry: PublicEdgeEntryBoth}
	with := func(edit func(*PublicEdge)) PublicEdge {
		e := direct
		e.Front = PublicEdgeFrontNone
		edit(&e)
		return e
	}
	host := func(edit func(*Host)) []Host {
		h := entered
		edit(&h)
		return []Host{control, h}
	}
	dns := func(proxied ...string) *PublicEdgeDNS {
		return &PublicEdgeDNS{ZoneID: "zone-1", Names: []string{"pilot.gregale.dev"}, Proxied: proxied}
	}
	for _, tc := range []struct {
		name    string
		edge    PublicEdge
		hosts   []Host
		wantErr string
	}{
		{name: "none", edge: with(func(*PublicEdge) {}), hosts: []Host{control, entered}},
		{name: "none with a proxied name", edge: with(func(e *PublicEdge) { e.DNS = dns("*.gregale.dev") }), hosts: []Host{control, entered}},
		{name: "cloudflare", edge: PublicEdge{ComputeNodes: true, Entry: PublicEdgeEntryBoth, Front: PublicEdgeFrontCloudflare}, hosts: host(func(h *Host) { h.EntryIPv4 = "" })},
		{name: "unknown", edge: with(func(e *PublicEdge) { e.Front = "fastly" }), hosts: []Host{control, entered}, wantErr: `"fastly" is not cloudflare or none`},
		{name: "on the tunnel", edge: PublicEdge{ComputeNodes: true, Front: PublicEdgeFrontNone}, hosts: []Host{control, entered}, wantErr: "none needs public_edge.compute_nodes and entry: both"},
		{name: "no entry address", edge: with(func(*PublicEdge) {}), hosts: host(func(h *Host) { h.EntryIPv4 = "" }), wantErr: "fleet.hosts[1].entry_ipv4"},
		{name: "the managed address", edge: with(func(*PublicEdge) {}), hosts: host(func(h *Host) { h.EntryIPv4 = "10.42.0.2" }), wantErr: "drops SSH"},
		{name: "the public address", edge: with(func(*PublicEdge) {}), hosts: host(func(h *Host) { h.EntryIPv4 = h.PublicIPv4 }), wantErr: "drops SSH"},
		{name: "a forwarded public address", edge: with(func(*PublicEdge) {}), hosts: host(func(h *Host) { h.EntryIPv4, h.PublicIPv4Forwarded = "", true })},
		{name: "a forwarded public address named again", edge: with(func(*PublicEdge) {}), hosts: host(func(h *Host) { h.EntryIPv4, h.PublicIPv4Forwarded = h.PublicIPv4, true })},
		{name: "forwarded without an address", edge: with(func(*PublicEdge) {}), hosts: host(func(h *Host) { h.PublicIPv4, h.PublicIPv4Forwarded = "", true }), wantErr: "public_ipv4_forwarded"},
		{name: "not canonical", edge: with(func(*PublicEdge) {}), hosts: host(func(h *Host) { h.EntryIPv4 = "198.51.100.02" }), wantErr: "canonical form"},
		{name: "IPv6", edge: with(func(*PublicEdge) {}), hosts: host(func(h *Host) { h.EntryIPv4 = "2001:db8::2" }), wantErr: "canonical form"},
		{name: "an entry address behind Cloudflare", edge: direct, hosts: []Host{control, entered}, wantErr: "under public_edge.front: none"},
		{name: "proxied names behind Cloudflare", edge: PublicEdge{ComputeNodes: true, Entry: PublicEdgeEntryBoth, DNS: dns("*.gregale.dev")}, hosts: host(func(h *Host) { h.EntryIPv4 = "" }), wantErr: "public_edge.dns.proxied"},
		{name: "a proxied name twice", edge: with(func(e *PublicEdge) { e.DNS = dns("a.gregale.dev", "a.gregale.dev") }), hosts: []Host{control, entered}, wantErr: "public_edge.dns.proxied[1]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.edge.validate(tc.hosts)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("validate = %v, want no errors", errs)
				}
			} else if !strings.Contains(errs.Error(), tc.wantErr) {
				t.Fatalf("validate = %v, want %q", errs, tc.wantErr)
			}
			want := tc.edge.Front == PublicEdgeFrontNone && tc.edge.DirectEntry()
			if got := tc.edge.NoFront(); got != want {
				t.Fatalf("NoFront = %v, want %v", got, want)
			}
		})
	}
}

// adr: 20261103163954 — a forwarded public_ipv4 (a GCP forwarding rule) is the
// entry address unless entry_ipv4 names another.
func TestHostEntryAddress(t *testing.T) {
	for _, tc := range []struct {
		host Host
		want string
	}{
		{Host{PublicIPv4: "203.0.113.2"}, ""},
		{Host{PublicIPv4: "203.0.113.2", PublicIPv4Forwarded: true}, "203.0.113.2"},
		{Host{PublicIPv4: "203.0.113.2", PublicIPv4Forwarded: true, EntryIPv4: "198.51.100.2"}, "198.51.100.2"},
		{Host{PublicIPv4: "203.0.113.2", EntryIPv4: "198.51.100.2"}, "198.51.100.2"},
	} {
		if got := tc.host.EntryAddress(); got != tc.want {
			t.Errorf("%+v.EntryAddress() = %q, want %q", tc.host, got, tc.want)
		}
	}
}

// withCertificate returns edge with its DNS settings copied and cert set.
func withCertificate(edge PublicEdge, cert *PublicEdgeCertificate) PublicEdge {
	dns := *edge.DNS
	dns.Certificate = cert
	edge.DNS = &dns
	return edge
}

func TestPublicEdgeCertificateCADefaultsToStaging(t *testing.T) {
	if got := (PublicEdgeCertificate{}).CAOrDefault(); got != EdgeCertificateCAStaging {
		t.Fatalf("CAOrDefault = %q, want %q", got, EdgeCertificateCAStaging)
	}
}

// adr: 20261110123517 — public_edge.overload extends the client limits, so it
// needs front: none.
func TestPublicEdgeOverload(t *testing.T) {
	control := Host{Name: "cp-1", Role: "control-plane"}
	entered := Host{Name: "cn-1", Role: "compute-only", Address: "10.42.0.2:50051", PublicIPv4: "203.0.113.2", EntryIPv4: "198.51.100.2"}
	noFront := PublicEdge{ComputeNodes: true, Entry: PublicEdgeEntryBoth, Front: PublicEdgeFrontNone}
	behindCloudflare := PublicEdge{ComputeNodes: true, Entry: PublicEdgeEntryBoth}
	for _, tc := range []struct {
		name     string
		edge     PublicEdge
		hosts    []Host
		overload string
		wantErr  string
	}{
		{name: "unset", edge: noFront, hosts: []Host{control, entered}},
		{name: "off behind Cloudflare", edge: behindCloudflare, hosts: []Host{control, {Name: "cn-1", Role: "compute-only", Address: "10.42.0.2:50051"}}, overload: PublicEdgeOverloadOff},
		{name: "observe", edge: noFront, hosts: []Host{control, entered}, overload: PublicEdgeOverloadObserve},
		{name: "on", edge: noFront, hosts: []Host{control, entered}, overload: PublicEdgeOverloadOn},
		{name: "on behind Cloudflare", edge: behindCloudflare, hosts: []Host{control, {Name: "cn-1", Role: "compute-only", Address: "10.42.0.2:50051"}}, overload: PublicEdgeOverloadOn, wantErr: "needs public_edge.front: none"},
		{name: "not a mode", edge: noFront, hosts: []Host{control, entered}, overload: "1", wantErr: `"1" is not off, observe or on`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := tc.edge
			e.Overload = tc.overload
			errs := e.validate(tc.hosts)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("validate = %v, want no errors", errs)
				}
				return
			}
			if !strings.Contains(errs.Error(), tc.wantErr) {
				t.Fatalf("validate = %v, want %q", errs, tc.wantErr)
			}
		})
	}
}

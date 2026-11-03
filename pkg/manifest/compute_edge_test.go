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

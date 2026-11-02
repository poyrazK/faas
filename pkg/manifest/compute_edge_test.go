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

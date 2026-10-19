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

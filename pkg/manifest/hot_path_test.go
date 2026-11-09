package manifest

import (
	"strings"
	"testing"
)

// The edge keep-alive needs the compute edges, and the frozen fast path needs
// the frozen tier.
func TestHotPathValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hot     HotPath
		edge    PublicEdge
		wantErr string
	}{
		{name: "off"},
		{name: "all on", hot: HotPath{LocalRequestIDJournal: true, RequestTelemetryMinuteCollapse: true, LocalVMMDDataSocket: true, EdgeKeepAlivePerNode: true, FrozenTier: true, FrozenFastPath: true, OwnerCache: true}, edge: PublicEdge{ComputeNodes: true}},
		{name: "keep-alive without compute edges", hot: HotPath{EdgeKeepAlivePerNode: true}, wantErr: "hot_path.edge_keepalive_per_node"},
		{name: "fast path without the tier", hot: HotPath{FrozenFastPath: true}, wantErr: "hot_path.frozen_fast_path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.hot.validate(tc.edge)
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

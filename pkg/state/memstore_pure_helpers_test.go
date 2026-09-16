package state

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

// TestMemStorePureHelpers pins the in-memory projections that have no
// database dependency: bounded node pagination, edge-rule host matching,
// and anomaly standard-deviation math.
// adr: 091 — in-memory edge-rule matching must stay aligned with Postgres.
func TestMemStorePureHelpers(t *testing.T) {
	t.Run("node page", func(t *testing.T) {
		m := NewMemStore()
		ctx := context.Background()
		for _, node := range []ComputeNode{
			{Name: "b-node", TargetURL: "unix:///run/vmmd.sock", Active: true},
			{Name: "a-node", TargetURL: "unix:///run/vmmd.sock", Active: false},
		} {
			if _, err := m.CreateComputeNode(ctx, node); err != nil {
				t.Fatalf("CreateComputeNode(%q): %v", node.Name, err)
			}
		}
		if got, err := m.ListComputeNodesPage(ctx, true, 0); err != nil || len(got) != 0 {
			t.Fatalf("zero limit = %v, %v; want empty", got, err)
		}
		got, err := m.ListComputeNodesPage(ctx, true, 1)
		if err != nil {
			t.Fatalf("ListComputeNodesPage: %v", err)
		}
		if len(got) != 1 || got[0].Name != "a-node" {
			t.Fatalf("limited page = %+v, want sorted a-node", got)
		}
		got, err = m.ListComputeNodesPage(ctx, false, 10)
		if err != nil || len(got) != 2 || got[0].Name != "b-node" {
			t.Fatalf("active page = %+v, %v; want b-node and default-local", got, err)
		}
	})

	t.Run("host matching", func(t *testing.T) {
		cases := []struct {
			pattern, host string
			want          bool
		}{
			{"*", "api.example.com", true},
			{"api.example.com", "api.example.com", true},
			{"*.example.com", "api.example.com", true},
			{"*.example.com", "example.com", false},
			{"*.example.com", "api.other.com", false},
			{"api.example.com", "other.example.com", false},
		}
		for _, tc := range cases {
			if got := matchHostPattern(tc.pattern, tc.host); got != tc.want {
				t.Errorf("matchHostPattern(%q, %q) = %v, want %v", tc.pattern, tc.host, got, tc.want)
			}
		}
	})

	t.Run("sqrt", func(t *testing.T) {
		for _, tc := range []struct {
			in, want float64
		}{
			{0, 0},
			{-4, 0},
			{9, 3},
			{1e6, 1e3},
		} {
			if got := sqrtFloat64(tc.in); math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("sqrtFloat64(%v) = %v, want %v", tc.in, got, tc.want)
			}
		}
	})

	t.Run("heartbeat stats", func(t *testing.T) {
		m := NewMemStore()
		ctx := context.Background()
		if err := m.AppendComputeNodeHeartbeatWithStats(ctx, "missing", time.Now(), time.Now(), "builder_tick", 1, 2); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing node heartbeat = %v, want ErrNotFound", err)
		}
		node, err := m.CreateComputeNode(ctx, ComputeNode{Name: "stats-node", TargetURL: "unix:///run/vmmd.sock", Active: true})
		if err != nil {
			t.Fatalf("CreateComputeNode: %v", err)
		}
		first := time.Now().Add(-time.Minute)
		second := first.Add(time.Second)
		if err := m.AppendComputeNodeHeartbeatWithStats(ctx, node.ID, first, first, "builder_tick", 12.5, 1024); err != nil {
			t.Fatalf("builder heartbeat: %v", err)
		}
		if err := m.AppendComputeNodeHeartbeatWithStats(ctx, node.ID, second, second, "vmmd_tick", 25, 2048); err != nil {
			t.Fatalf("vmmd heartbeat: %v", err)
		}
		if err := m.AppendComputeNodeHeartbeatWithStats(ctx, node.ID, second, second, "duplicate", 30, 4096); !errors.Is(err, ErrConflict) {
			t.Fatalf("duplicate heartbeat = %v, want ErrConflict", err)
		}
		// A node history can exist before its compute-node projection is
		// populated; keep the silent-row projection contract covered.
		m.computeNodeHeartbeats["silent-node"] = nil

		all, err := m.LatestHeartbeatStats(ctx)
		if err != nil {
			t.Fatalf("LatestHeartbeatStats: %v", err)
		}
		var found, silent bool
		for _, row := range all {
			switch row.NodeID {
			case node.ID:
				found = true
				if !row.ReceivedAt.Equal(second) || row.CPUPct60s == nil || *row.CPUPct60s != 25 || row.DiskUsedBytes == nil || *row.DiskUsedBytes != 2048 {
					t.Errorf("latest stats = %+v, want second vmmd heartbeat", row)
				}
			default:
				if row.CPUPct60s == nil && row.DiskUsedBytes == nil {
					silent = true
				}
			}
		}
		if !found || !silent {
			t.Errorf("LatestHeartbeatStats = %+v, want stats-node and a silent node", all)
		}
		builders, err := m.LatestBuilderHeartbeatStats(ctx)
		if err != nil {
			t.Fatalf("LatestBuilderHeartbeatStats: %v", err)
		}
		if len(builders) != 1 || builders[0].NodeID != node.ID || builders[0].ReceivedAt != first || builders[0].CPUPct60s == nil || *builders[0].CPUPct60s != 12.5 {
			t.Errorf("builder stats = %+v, want the first builder heartbeat only", builders)
		}
	})
}

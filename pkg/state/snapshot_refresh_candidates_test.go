// adr: 20261106214916

package state

import (
	"context"
	"testing"
	"time"
)

func TestMemListSnapshotRefreshCandidates(t *testing.T) {
	m := NewMemStore()
	now := time.Now().UTC()
	put := func(id, app, node, st, mode string, parkedAgo time.Duration) {
		m.instances[id] = Instance{ID: id, AppID: app, DeploymentID: "dep-" + app, NodeID: node, State: st,
			Mode: mode, Kind: "wake", ParkedAt: now.Add(-parkedAgo)}
	}
	put("i1", "recent", "a", "parked", "normal", time.Hour)
	put("i2", "moved", "a", "parked", "normal", 2*time.Hour)
	put("i3", "moved", "b", "parked", "normal", 30*time.Minute)
	put("i4", "live", "a", "parked", "normal", 3*time.Hour)
	put("i5", "live", "b", "running", "normal", 0)
	put("i6", "old", "a", "parked", "normal", 10*24*time.Hour)
	put("i7", "mirror", "a", "parked", "mirror", time.Hour)

	got, err := m.ListSnapshotRefreshCandidates(context.Background(), "a", now.Add(-168*time.Hour), 10)
	if err != nil || len(got) != 2 || got[0].AppID != "recent" || got[0].Live || got[1].AppID != "live" || !got[1].Live {
		t.Fatalf("node a candidates = %+v (err %v), want recent then live", got, err)
	}
	if b, _ := m.ListSnapshotRefreshCandidates(context.Background(), "b", now.Add(-168*time.Hour), 10); len(b) != 1 || b[0].AppID != "moved" {
		t.Fatalf("node b candidates = %+v, want moved", b)
	}
}

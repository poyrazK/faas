package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgAppsListVersionTracksListedRows checks the version schedd's app
// snapshot compares: it holds while nothing changes and moves with every
// write to a listed app, including columns the runtime-generation trigger
// skips, a new app, an app joining a node and a soft delete.
func TestPgAppsListVersionTracksListedRows(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	node := resolveDefaultLocal(t, ctx, s)
	read := func(nodeID string) state.AppsListVersion {
		t.Helper()
		v, err := s.AppsListVersion(ctx, nodeID)
		if err != nil {
			t.Fatalf("AppsListVersion(%q): %v", nodeID, err)
		}
		return v
	}
	_, a := seedPgAccountAndApp(t, s, ctx)
	all := read("")
	if again := read(""); again != all {
		t.Fatalf("version moved without a write: %+v then %+v", all, again)
	}

	if _, err := pool.Exec(ctx, `update apps set last_scale_out_at = now() where id = $1`, a.ID); err != nil {
		t.Fatalf("touch last_scale_out_at: %v", err)
	}
	scaled := read("")
	if scaled == all {
		t.Fatalf("version %+v did not move with last_scale_out_at", scaled)
	}

	_, b := seedPgAccountAndApp(t, s, ctx)
	added := read("")
	if added.Count != scaled.Count+1 {
		t.Fatalf("count %d after a new app, want %d", added.Count, scaled.Count+1)
	}

	before := read(node)
	if err := s.SetAppNodeID(ctx, a.ID, node); err != nil {
		t.Fatalf("SetAppNodeID: %v", err)
	}
	if owned := read(node); owned.Count != before.Count+1 {
		t.Fatalf("node count %d after the app joined, want %d", owned.Count, before.Count+1)
	}

	if _, err := s.SoftDeleteAppCascade(ctx, b.ID); err != nil {
		t.Fatalf("SoftDeleteAppCascade: %v", err)
	}
	if deleted := read(""); deleted.Count != added.Count-1 {
		t.Fatalf("count %d after a soft delete, want %d", deleted.Count, added.Count-1)
	}
}

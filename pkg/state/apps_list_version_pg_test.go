package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgAppsListVersionTracksListedRows checks the version schedd's app
// snapshot compares: it holds while nothing changes and across scale stamps
// (ADR 20261103094355), and moves with any other write to a listed app, a new
// app, an app joining a node and a soft delete.
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

	if _, err := pool.Exec(ctx, `update apps set last_scale_out_at = now(), last_scale_in_at = now() where id = $1`, a.ID); err != nil {
		t.Fatalf("touch scale stamps: %v", err)
	}
	if stamped := read(""); stamped != all {
		t.Fatalf("version moved with the scale stamps alone: %+v then %+v", all, stamped)
	}

	if _, err := pool.Exec(ctx, `update apps set maintenance_mode = not maintenance_mode where id = $1`, a.ID); err != nil {
		t.Fatalf("toggle maintenance_mode: %v", err)
	}
	scaled := read("")
	if scaled == all {
		t.Fatalf("version %+v did not move with maintenance_mode", scaled)
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

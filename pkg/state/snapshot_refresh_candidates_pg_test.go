// adr: 20261106214916

package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgListSnapshotRefreshCandidates: an app is a candidate on the node of
// its latest park in the window, most recent first, and is marked live when
// it has a live instance anywhere.
func TestPgListSnapshotRefreshCandidates(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	nodeA := resolveDefaultLocal(t, ctx, s)
	var nodeB string
	if err := pool.QueryRow(ctx, `
		insert into compute_nodes (name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb)
		values ('refresh-b', 'unix:///run/faas/vmmd-b.sock', 16, 64000, 100, 47600) returning id`).Scan(&nodeB); err != nil {
		t.Fatalf("insert node b: %v", err)
	}
	now := time.Now().UTC()
	insert := func(appID, deploymentID, node, st, mode string, parkedAgo time.Duration) {
		t.Helper()
		var parkedAt *time.Time
		if st == "parked" {
			at := now.Add(-parkedAgo)
			parkedAt = &at
		}
		if _, err := pool.Exec(ctx, `
			insert into instances (id, app_id, deployment_id, node_id, state, ram_mb, started_at, parked_at, mode)
			values ($1, $2, $3, $4, $5, 256, $6, $7, $8)`,
			uuid.NewString(), appID, deploymentID, node, st, now.Add(-parkedAgo-time.Minute), parkedAt, mode); err != nil {
			t.Fatalf("insert %s %s instance: %v", st, mode, err)
		}
	}
	app := func() (string, string) {
		_, a := seedPgAccountAndApp(t, s, ctx)
		return a.ID, seedPgDeployment(t, s, ctx, a).ID
	}
	recent, recentDep := app()
	insert(recent, recentDep, nodeA, "parked", "normal", 2*time.Hour)
	insert(recent, recentDep, nodeA, "parked", "normal", time.Hour)
	moved, movedDep := app()
	insert(moved, movedDep, nodeA, "parked", "normal", 2*time.Hour)
	insert(moved, movedDep, nodeB, "parked", "normal", 30*time.Minute)
	live, liveDep := app()
	insert(live, liveDep, nodeA, "parked", "normal", 3*time.Hour)
	insert(live, liveDep, nodeB, "running", "normal", 0)
	old, oldDep := app()
	insert(old, oldDep, nodeA, "parked", "normal", 10*24*time.Hour)
	mirror, mirrorDep := app()
	insert(mirror, mirrorDep, nodeA, "parked", "mirror", time.Hour)

	since := now.Add(-168 * time.Hour)
	got, err := s.ListSnapshotRefreshCandidates(ctx, nodeA, since, 10)
	if err != nil {
		t.Fatalf("ListSnapshotRefreshCandidates: %v", err)
	}
	want := []state.SnapshotRefreshCandidate{{AppID: recent, DeploymentID: recentDep}, {AppID: live, DeploymentID: liveDep, Live: true}}
	if len(got) != len(want) {
		t.Fatalf("node a candidates = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].AppID != want[i].AppID || got[i].DeploymentID != want[i].DeploymentID || got[i].Live != want[i].Live {
			t.Fatalf("node a candidate %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if !got[0].ParkedAt.After(got[1].ParkedAt) || now.Sub(got[0].ParkedAt) > time.Hour+time.Minute {
		t.Fatalf("node a candidates not newest park first: %+v", got)
	}
	// The parked instance's start: when its vCPU count was chosen.
	if d := got[0].ParkedAt.Sub(got[0].StartedAt); d < time.Minute-time.Second || d > time.Minute+time.Second {
		t.Fatalf("node a candidate started at %v, parked at %v; want a minute before the park", got[0].StartedAt, got[0].ParkedAt)
	}
	if b, err := s.ListSnapshotRefreshCandidates(ctx, nodeB, since, 10); err != nil || len(b) != 1 || b[0].AppID != moved {
		t.Fatalf("node b candidates = %+v (err %v), want only the app that last parked there", b, err)
	}
	if one, err := s.ListSnapshotRefreshCandidates(ctx, nodeA, since, 1); err != nil || len(one) != 1 || one[0].AppID != recent {
		t.Fatalf("limit 1 = %+v (err %v), want the most recent park", one, err)
	}
}

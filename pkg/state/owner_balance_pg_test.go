package state_test

// adr: 20261118160000

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgOwnerBalanceCandidates: an idle app with a stored live snapshot is a
// candidate with that snapshot; one whose live deployment has no stored
// snapshot is not, nor is one moved after the cutoff.
func TestPgOwnerBalanceCandidates(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	nodeID := resolveDefaultLocal(t, ctx, s)
	_, withSnap, withSnapDep := seedLiveDeploy(t, s, ctx, "balance-snap", "snap")
	_, noSnap, _ := seedLiveDeploy(t, s, ctx, "balance-nosnap", "nosnap")
	_, moved, _ := seedLiveDeploy(t, s, ctx, "balance-moved", "moved")
	for _, id := range []string{withSnap, noSnap, moved} {
		if _, err := pool.Exec(ctx, `update apps set node_id = $2, status = 'active' where id = $1`, id, nodeID); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: withSnapDep, FCVersion: "fc-balance", MemBytes: 1024, DiskBytes: 2048,
		StorageKey: "snap/" + withSnapDep + "/captures/c1/v2/mem",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The cutoff is now: the app moved a moment ago is not idle yet; the
	// others were created before it and never moved.
	if _, err := pool.Exec(ctx, `update apps set reassigned_at = now() + interval '30 seconds' where id = $1`, moved); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListOwnerBalanceCandidates(ctx, nodeID, time.Now().Add(5*time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]state.OwnerBalanceCandidate{}
	for _, c := range got {
		ids[c.ID] = c
	}
	c, ok := ids[withSnap]
	if !ok || !c.HasLiveDeployment || c.Snapshot.ID != snap.ID || c.Snapshot.StorageKey == "" {
		t.Fatalf("candidate for the app with a snapshot = %+v (found %t)", c, ok)
	}
	if _, ok := ids[noSnap]; ok {
		t.Fatal("an app whose live deployment has no stored snapshot is a candidate")
	}
	if _, ok := ids[moved]; ok {
		t.Fatal("an app moved after the cutoff is a candidate")
	}
}

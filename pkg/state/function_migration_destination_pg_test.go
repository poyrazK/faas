//go:build !no_pg

package state

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// adr: 1664
func TestFunctionDestinationPg(t *testing.T) {
	t.Run("first claim serialization", functionDestinationFirstClaimSerialization)
	t.Run("capacity", func(t *testing.T) {
		_, pool := runtimeCapturePGStore(t)
		functionDestinationCapacity(t, NewPgStore(pool))
	})
	for _, scenario := range []string{"target restart", "source restart", "security policy", "lease expiry"} {
		t.Run(scenario, func(t *testing.T) { s, _ := runtimeCapturePGStore(t); functionDestinationDrift(t, s, scenario) })
	}
	for _, test := range []struct {
		name string
		run  func(*testing.T, functionDestinationTestStore)
	}{{"lifecycle", functionDestinationLifecycle}, {"rollback", functionDestinationRollback}, {"commit race", functionDestinationCommitRace}} {
		t.Run(test.name, func(t *testing.T) { s, _ := runtimeCapturePGStore(t); test.run(t, s) })
	}
}

// An ordinary admission must wait for an issuing destination even before its
// first pending row is visible. Otherwise both transactions can spend the same
// headroom. The lifecycle capacity case checks the claim once it is visible.
func functionDestinationFirstClaimSerialization(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	ins, g, _, _ := functionDestinationFixture(t, s)
	node, err := s.ComputeNodeByID(t.Context(), g.Binding.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	node.ID, node.Name = "", "function-first-claim-"+uuid.NewString()
	node, err = s.UpsertComputeNode(t.Context(), node)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	issuer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = issuer.Rollback(ctx) }()
	if _, err := issuer.Exec(ctx, "SELECT pg_advisory_xact_lock(1852793957,hashtext($1::text))", node.ID); err != nil {
		t.Fatal(err)
	}
	admitted := make(chan error, 1)
	go func() {
		_, err := s.CreateInstance(ctx, ins.AppID, ins.DeploymentID, string(StateWaking), ins.RAMMB, node.ID, uuid.NewString())
		admitted <- err
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-admitted:
			t.Fatal("admission bypassed an uncommitted first destination claim", err)
		case <-ctx.Done():
			t.Fatal("admission never reached the destination capacity fence", ctx.Err())
		case <-ticker.C:
			var waiting bool
			err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND classid=1852793957::oid AND objid=hashtext($1::text)::oid)", node.ID).Scan(&waiting)
			if err != nil {
				t.Fatal(err)
			}
			if !waiting {
				continue
			}
			if err := issuer.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-admitted; err != nil {
				t.Fatal("released claim prevented ordinary admission", err)
			}
			return
		}
	}
}

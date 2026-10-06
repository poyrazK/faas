//go:build !no_pg

// adr: 421
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

// ADR-421: a lifecycle update that has already locked a node must win over
// a transfer that discovered eligibility earlier. The transfer waits for the
// writer and then reads its committed verdict rather than a stale snapshot.
func TestOwnershipRecoveryPostgresLifecycleWriterFencesTransfer(t *testing.T) {
	for _, tc := range []struct {
		name         string
		changeSource bool
	}{
		{"source_recovers", true}, {"destination_drains", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := db.MigrateUp(ctx, pool); err != nil {
				t.Fatal(err)
			}
			store := state.NewPgStore(pool)
			fx := conformance.Seed(t, store)
			destination, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetAppNodeID(ctx, fx.App.ID, fx.Node.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.SetComputeNodeActive(ctx, fx.Node.ID, false); err != nil {
				t.Fatal(err)
			}
			writer, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Rollback(context.Background()) }()
			id, lifecycle := destination.ID, state.NodeLifecycleDraining
			if tc.changeSource {
				id, lifecycle = fx.Node.ID, state.NodeLifecycleActive
			}
			if _, err := writer.Exec(ctx, "UPDATE compute_nodes SET lifecycle=$2 WHERE id=$1", id, string(lifecycle)); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				done <- store.ReassignOrphanedAppOwner(ctx, fx.App.ID, fx.Node.ID, destination.ID, 60)
			}()
			// Observe the actual database lock wait instead of guessing when the
			// goroutine reached its query. No production hook is needed.
			deadline := time.Now().Add(3 * time.Second)
			for {
				var waiting bool
				err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
                    WHERE datname=current_database() AND wait_event_type='Lock'
                    AND query LIKE '%name: LockOwnershipRecoveryNodes%')`).Scan(&waiting)
				if err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("transfer did not wait for lifecycle writer: %v", err)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("transfer never reached lifecycle lock")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := writer.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, state.ErrConflict) {
				t.Fatalf("lifecycle writer not respected: %v", err)
			}
			app, err := store.AppByID(ctx, fx.App.ID)
			if err != nil || app.NodeID != fx.Node.ID || app.ReassignedAt != nil {
				t.Fatalf("conflicting transfer mutated ownership: %+v %v", app, err)
			}
		})
	}
}

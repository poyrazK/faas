//go:build !no_pg

// adr: 1527
package state_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func lifecycleCompletionPGStore(t *testing.T) (*state.PgStore, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	// A long shared suite can outlive the template node's heartbeat.
	node, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.HeartbeatComputeNode(t.Context(), node.ID); err != nil {
		t.Fatal(err)
	}
	return store, pool
}

func TestNodeLifecycleCompletionPostgres(t *testing.T) {
	runLifecycleCompletionTests(t, func(t *testing.T) lifecycleCompletionTestStore {
		store, _ := lifecycleCompletionPGStore(t)
		return store
	})
}

func TestNodeLifecycleCompletionPostgresAtomicFailures(t *testing.T) {
	for _, fault := range []struct{ name, table, event, body, timing string }{
		{"node_write", "compute_nodes", "UPDATE", "RAISE EXCEPTION 'node unavailable';", "BEFORE"},
		{"suppressed_node", "compute_nodes", "UPDATE", "RETURN NULL;", "BEFORE"},
		{"audit_write", "audit_event_outbox", "INSERT", "RAISE EXCEPTION 'audit unavailable';", "BEFORE"},
		{"suppressed_audit", "audit_event_outbox", "INSERT", "RETURN NULL;", "BEFORE"},
		{"receipt_write", "node_lifecycle_completions", "INSERT", "RAISE EXCEPTION 'receipt unavailable';", "BEFORE"},
		{"suppressed_receipt", "node_lifecycle_completions", "INSERT", "RETURN NULL;", "BEFORE"},
		{"commit", "compute_nodes", "UPDATE", "RAISE EXCEPTION 'deferred failure';", "AFTER"},
	} {
		t.Run(fault.name, func(t *testing.T) {
			store, pool := lifecycleCompletionPGStore(t)
			node := completionNode(t, store, state.NodeLifecycleDraining)
			ddl := "CREATE FUNCTION completion_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN " + fault.body + " RETURN NEW; END $$; CREATE "
			if fault.timing == "AFTER" {
				ddl += "CONSTRAINT "
			}
			ddl += "TRIGGER completion_fault " + fault.timing + " " + fault.event + " ON " + fault.table
			if fault.timing == "AFTER" {
				ddl += " DEFERRABLE INITIALLY DEFERRED"
			}
			ddl += " FOR EACH ROW EXECUTE FUNCTION completion_fault()"
			if _, err := pool.Exec(t.Context(), ddl); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CompleteNodeLifecycle(t.Context(), completionInput(node)); err == nil {
				t.Fatal("fault returned success")
			}
			got := completionRead(t, store, node.ID)
			if got.Lifecycle != node.Lifecycle || got.LifecycleGeneration != node.LifecycleGeneration || got.DrainCompletedAt != nil {
				t.Fatalf("partial node completion: %+v", got)
			}
			var receipts, audits int
			if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM node_lifecycle_completions),(SELECT count(*) FROM audit_event_outbox)`).Scan(&receipts, &audits); err != nil {
				t.Fatal(err)
			}
			if receipts != 0 || audits != 0 {
				t.Fatalf("partial receipts=%d audits=%d", receipts, audits)
			}
			if _, err := pool.Exec(t.Context(), "DROP TRIGGER completion_fault ON "+fault.table); err != nil {
				t.Fatal(err)
			}
			if r, err := store.CompleteNodeLifecycle(t.Context(), completionInput(node)); err != nil || r.Replayed {
				t.Fatalf("retry after rollback: %+v %v", r, err)
			}
		})
	}
}

func TestNodeLifecycleCompletionPostgresGuardsAndDeletion(t *testing.T) {
	store, pool := lifecycleCompletionPGStore(t)
	node := completionNode(t, store, state.NodeLifecycleDraining)
	// Direct metadata updates preserve the cycle; same-state initiation restarts it.
	if _, err := pool.Exec(t.Context(), `UPDATE compute_nodes SET name=name||'-refresh' WHERE id=$1`, node.ID); err != nil {
		t.Fatal(err)
	}
	if got := completionRead(t, store, node.ID); got.LifecycleGeneration != node.LifecycleGeneration {
		t.Fatal("metadata changed cycle")
	}
	if _, err := pool.Exec(t.Context(), `UPDATE compute_nodes SET drain_initiated_at=clock_timestamp() WHERE id=$1`, node.ID); err != nil {
		t.Fatal(err)
	}
	next := completionRead(t, store, node.ID)
	if next.LifecycleGeneration != node.LifecycleGeneration+1 {
		t.Fatal("same-state cycle not advanced")
	}
	if _, err := store.CompleteNodeLifecycle(t.Context(), completionInput(node)); !errors.Is(err, state.ErrNodeLifecycleChanged) {
		t.Fatalf("old direct-writer cycle accepted: %v", err)
	}
	for _, sql := range []string{
		`UPDATE compute_nodes SET lifecycle_generation=1 WHERE id=$1`,
		`UPDATE compute_nodes SET lifecycle='maintenance' WHERE id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), sql, node.ID); err == nil {
			t.Fatalf("unfenced writer accepted: %s", sql)
		}
	}
	in := completionInput(next)
	r, err := store.CompleteNodeLifecycle(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE node_lifecycle_completions SET drained_count=999 WHERE node_id=$1`,
		`DELETE FROM node_lifecycle_completions WHERE node_id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), sql, node.ID); err == nil {
			t.Fatalf("receipt mutation accepted: %s", sql)
		}
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM compute_nodes WHERE id=$1`, node.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := store.CompleteNodeLifecycle(t.Context(), in)
	if err != nil || !replay.Replayed || replay.AuditOutboxID != r.AuditOutboxID || !replay.CompletedAt.Equal(r.CompletedAt) || replay.NodeName != r.NodeName {
		t.Fatalf("deletion changed original receipt: %+v %v", replay, err)
	}
}

func waitLifecycleCompletionBlocked(t *testing.T, pool *pgxpool.Pool, blocker uint32) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		var blocked bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::bigint=ANY(pg_blocking_pids(pid)::bigint[]))`, blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-deadline:
			t.Fatal("writer never reached transaction barrier")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestNodeLifecycleCompletionPostgresCapacityLockOrder(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) { testLifecycleCompletionCapacityLockOrder(t, enabled) })
	}
}

func testLifecycleCompletionCapacityLockOrder(t *testing.T, enabled bool) {
	t.Helper()
	store, pool := lifecycleCompletionPGStore(t)
	if _, err := pool.Exec(t.Context(), `UPDATE service_capacity_policy SET enabled=$1 WHERE singleton`, enabled); err != nil {
		t.Fatal(err)
	}
	node := completionNode(t, store, state.NodeLifecycleDraining)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), `SELECT singleton FROM service_capacity_policy WHERE singleton FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() { _, err := store.CompleteNodeLifecycle(t.Context(), completionInput(node)); completed <- err }()
	waitLifecycleCompletionBlocked(t, pool, tx.Conn().PgConn().PID())
	// Completion must wait for capacity before taking the node row.
	if _, err := tx.Exec(t.Context(), `SELECT id FROM compute_nodes WHERE id=$1 FOR UPDATE NOWAIT`, node.ID); err != nil {
		t.Fatalf("completion inverted capacity/node locks: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
}

func TestNodeLifecycleCompletionPostgresConcurrentResidentTransition(t *testing.T) {
	// A write admitted before the transaction obtains capacity must be visible
	// to its final check, including resident rows omitted by the runner's list.
	for _, resident := range []string{"running", "warm", "frozen"} {
		t.Run(resident, func(t *testing.T) {
			store, pool := lifecycleCompletionPGStore(t)
			appID, depID := completionApplication(t, store)
			node := completionNode(t, store, state.NodeLifecycleActive)
			ins, err := store.CreateInstance(t.Context(), appID, depID, "cold_booting", 128, node.ID, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			node = completionTransition(t, store, node, state.NodeLifecycleDraining)
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			// Exercise the real statement-level admission lock, not a synthetic
			// test mutex. Hold the resident transition uncommitted while completion starts.
			if _, err := tx.Exec(t.Context(), `UPDATE instances SET state=$2 WHERE id=$1`, ins.ID, resident); err != nil {
				t.Fatal(err)
			}
			completed := make(chan error, 1)
			go func() { _, err := store.CompleteNodeLifecycle(t.Context(), completionInput(node)); completed <- err }()
			waitLifecycleCompletionBlocked(t, pool, tx.Conn().PgConn().PID())
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := <-completed; !errors.Is(err, state.ErrConflict) {
				t.Fatalf("ignored concurrent %s admission: %v", resident, err)
			}
			if got := completionRead(t, store, node.ID); got.LifecycleGeneration != node.LifecycleGeneration || got.Lifecycle != node.Lifecycle {
				t.Fatalf("completed with resident work: %+v", got)
			}
		})
	}
}

func lifecycleCompletionMigration(t *testing.T) (string, string) {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	body, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../migrations/20261017140000001_node_lifecycle_completion.sql"))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(body), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("migration missing down section")
	}
	return parts[0], parts[1]
}

func TestNodeLifecycleCompletionPostgresDowngrade(t *testing.T) {
	up, down := lifecycleCompletionMigration(t)
	for _, scenario := range []string{"unused", "advanced_generation", "completed_receipt"} {
		t.Run(scenario, func(t *testing.T) {
			store, pool := lifecycleCompletionPGStore(t)
			if scenario != "unused" {
				node := completionNode(t, store, state.NodeLifecycleDraining)
				if scenario == "advanced_generation" {
					completionTransition(t, store, node, state.NodeLifecycleUnavailable)
				} else {
					if _, err := store.CompleteNodeLifecycle(t.Context(), completionInput(node)); err != nil {
						t.Fatal(err)
					}
					if _, err := pool.Exec(t.Context(), `DELETE FROM compute_nodes WHERE id=$1`, node.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, err := pool.Exec(t.Context(), down)
			if scenario != "unused" {
				if err == nil {
					t.Fatal("downgrade discarded completion evidence")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), up); err != nil {
				t.Fatal(err)
			}
		})
	}
}

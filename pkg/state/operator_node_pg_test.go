//go:build !no_pg

// adr: 1526
package state_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func operatorNodePGStore(t *testing.T) (*state.PgStore, *pgxpool.Pool) {
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

func TestOperatorNodePostgres(t *testing.T) {
	runNodeIntentTests(t, func(t *testing.T) nodeIntentTestStore { store, _ := operatorNodePGStore(t); return store })
}

func TestOperatorNodePostgresAtomicFailures(t *testing.T) {
	for _, fault := range []struct{ name, table, event, body, timing string }{
		{"node_write", "compute_nodes", "UPDATE", "RAISE EXCEPTION 'node unavailable';", "BEFORE"},
		{"suppressed_node", "compute_nodes", "UPDATE", "RETURN NULL;", "BEFORE"},
		{"audit_write", "audit_event_outbox", "INSERT", "RAISE EXCEPTION 'audit unavailable';", "BEFORE"},
		{"suppressed_audit", "audit_event_outbox", "INSERT", "RETURN NULL;", "BEFORE"},
		{"receipt_write", "operator_node_executions", "INSERT", "RAISE EXCEPTION 'receipt unavailable';", "BEFORE"},
		{"suppressed_receipt", "operator_node_executions", "INSERT", "RETURN NULL;", "BEFORE"},
		{"intent_write", "operator_intents", "UPDATE", "RAISE EXCEPTION 'intent unavailable';", "BEFORE"},
		{"suppressed_intent", "operator_intents", "UPDATE", "RETURN NULL;", "BEFORE"},
		{"commit", "operator_intents", "UPDATE", "RAISE EXCEPTION 'deferred commit failure';", "AFTER"},
	} {
		t.Run(fault.name, func(t *testing.T) {
			store, pool := operatorNodePGStore(t)
			node, i := seedNodeIntent(t, store, state.OperatorIntentKindNodeDrain, state.NodeLifecycleActive)
			// Fixed test-controlled DDL, never an application SQL construction path.
			ddl := "CREATE FUNCTION node_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN " + fault.body + " RETURN NEW; END $$; CREATE "
			if fault.timing == "AFTER" {
				ddl += "CONSTRAINT "
			}
			ddl += "TRIGGER node_fault " + fault.timing + " " + fault.event + " ON " + fault.table
			if fault.timing == "AFTER" {
				ddl += " DEFERRABLE INITIALLY DEFERRED"
			}
			ddl += " FOR EACH ROW EXECUTE FUNCTION node_fault()"
			if _, err := pool.Exec(t.Context(), ddl); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ExecuteOperatorNode(t.Context(), i.ID, i.ClaimToken); err == nil {
				t.Fatal("fault reported success")
			}
			current, err := store.NodeGet(t.Context(), node.ID)
			if err != nil || current.Lifecycle != state.NodeLifecycleActive || current.DrainInitiatedAt != nil {
				t.Fatalf("partial node change: %+v %v", current, err)
			}
			row, err := store.GetOperatorIntent(t.Context(), i.ID)
			if err != nil || row.Status != state.OperatorIntentRunning || row.FinishedAt != nil || row.CompletionAuditOutboxID != 0 {
				t.Fatalf("partial completion: %+v %v", row, err)
			}
			var receipts, audits int
			if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM operator_node_executions),(SELECT count(*) FROM audit_event_outbox)`).Scan(&receipts, &audits); err != nil {
				t.Fatal(err)
			}
			if receipts != 0 || audits != 0 {
				t.Fatalf("partial receipt=%d audit=%d", receipts, audits)
			}
			if _, err := pool.Exec(t.Context(), "DROP TRIGGER node_fault ON "+fault.table); err != nil {
				t.Fatal(err)
			}
			if r := executeNodeIntent(t, store, i); r.Status != state.OperatorIntentSucceeded || r.Replayed {
				t.Fatalf("rollback retry: %+v", r)
			}
		})
	}
}

func TestOperatorNodePostgresLegacyAndReceiptGuards(t *testing.T) {
	store, pool := operatorNodePGStore(t)
	_, i := seedNodeIntent(t, store, state.OperatorIntentKindNodeDrain, state.NodeLifecycleActive)
	if _, err := pool.Exec(t.Context(), `UPDATE operator_intents SET status='succeeded',finished_at=now() WHERE id=$1`, i.ID); err == nil {
		t.Fatal("legacy completion without receipt accepted")
	}
	executeNodeIntent(t, store, i)
	for _, sql := range []string{
		`UPDATE operator_node_executions SET changed=false WHERE intent_id=$1`,
		`DELETE FROM operator_node_executions WHERE intent_id=$1`,
		`UPDATE operator_intents SET status='failed',error='obsolete worker' WHERE id=$1`,
		`UPDATE operator_intents SET status='pending',claim_token=NULL WHERE id=$1`,
		`UPDATE operator_intents SET target_id='replacement' WHERE id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), sql, i.ID); err == nil {
			t.Fatalf("receipt mutation accepted: %s", sql)
		}
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM compute_nodes WHERE id=$1`, i.TargetID); err != nil {
		t.Fatal(err)
	}
	if r := executeNodeIntent(t, store, i); !r.Replayed || r.Status != state.OperatorIntentSucceeded {
		t.Fatalf("target deletion changed receipt: %+v", r)
	}
}

func nodeMigrationParts(t *testing.T) (string, string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "migrations", "20261017130000001_operator_node_execution.sql"))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(body), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("missing migration boundary")
	}
	return parts[0], parts[1]
}

func TestOperatorNodePostgresMigrationBoundary(t *testing.T) {
	up, down := nodeMigrationParts(t)
	t.Run("quarantine_and_preserve_legacy", func(t *testing.T) {
		store, pool := operatorNodePGStore(t)
		if _, err := pool.Exec(t.Context(), down); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, status := range []string{"pending", "running", "succeeded"} {
			id, err := store.InsertOperatorIntent(t.Context(), state.OperatorIntentKindNodeDrain, "legacy-node", nil, "22222222-2222-2222-2222-222222222222", "legacy", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE operator_intents SET status=$2,claim_token='33333333-3333-3333-3333-333333333333' WHERE id=$1`, id, status); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if _, err := pool.Exec(t.Context(), up); err != nil {
			t.Fatal(err)
		}
		for idx, id := range ids {
			row, err := store.GetOperatorIntent(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if idx < 2 && (row.Status != state.OperatorIntentOutcomeUnknown || row.ClaimToken != "" || row.FinishedAt == nil) {
				t.Fatalf("not quarantined: %+v", row)
			}
			if idx == 2 && row.Status != state.OperatorIntentSucceeded {
				t.Fatalf("terminal history changed: %+v", row)
			}
		}
		if n, err := store.ReclaimStuckRunningOperatorIntents(t.Context(), time.Now().Add(time.Hour)); err != nil || n != 0 {
			t.Fatalf("quarantined rows reclaimed: %d %v", n, err)
		}
		if _, err := pool.Exec(t.Context(), down); err == nil {
			t.Fatal("downgrade discarded unresolved evidence")
		}
	})
	t.Run("receipts_block_downgrade", func(t *testing.T) {
		store, pool := operatorNodePGStore(t)
		_, i := seedNodeIntent(t, store, state.OperatorIntentKindNodeDrain, state.NodeLifecycleActive)
		executeNodeIntent(t, store, i)
		if _, err := pool.Exec(t.Context(), down); err == nil {
			t.Fatal("downgrade discarded receipt")
		}
	})
}

func waitOperatorNodeBlocked(t *testing.T, pool *pgxpool.Pool, blocker uint32) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		var blocked bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::bigint=ANY(pg_blocking_pids(pid)::bigint[]))`, blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-deadline:
			t.Fatal("operation never reached transaction barrier")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestOperatorNodePostgresReclaimSerialization(t *testing.T) {
	for _, first := range []string{"execute", "reclaim"} {
		t.Run(first+"_first", func(t *testing.T) {
			store, pool := operatorNodePGStore(t)
			node, i := seedNodeIntent(t, store, state.OperatorIntentKindNodeDrain, state.NodeLifecycleActive)
			locker, err := pool.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer locker.Release()
			if _, err := locker.Exec(t.Context(), `SELECT pg_advisory_lock(1367001)`); err != nil {
				t.Fatal(err)
			}
			defer func() { _, _ = locker.Exec(context.Background(), `SELECT pg_advisory_unlock(1367001)`) }()
			table, condition := "compute_nodes", ""
			if first == "reclaim" {
				table, condition = "operator_intents", " WHEN (NEW.status='pending')"
			}
			if _, err := pool.Exec(t.Context(), `CREATE FUNCTION node_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(1367001); RETURN NEW; END $$; CREATE TRIGGER node_barrier BEFORE UPDATE ON `+table+` FOR EACH ROW`+condition+` EXECUTE FUNCTION node_barrier()`); err != nil {
				t.Fatal(err)
			}
			type executed struct {
				result state.OperatorNodeResult
				err    error
			}
			type reclaimed struct {
				count int
				err   error
			}
			execution, reclaim := make(chan executed, 1), make(chan reclaimed, 1)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			execute := func() { r, err := store.ExecuteOperatorNode(ctx, i.ID, i.ClaimToken); execution <- executed{r, err} }
			reclaimClaim := func() {
				n, err := store.ReclaimStuckRunningOperatorIntents(ctx, time.Now().Add(time.Hour))
				reclaim <- reclaimed{n, err}
			}
			if first == "execute" {
				go execute()
			} else {
				go reclaimClaim()
			}
			waitOperatorNodeBlocked(t, pool, locker.Conn().PgConn().PID())
			if first == "execute" {
				go reclaimClaim()
			} else {
				go execute()
			}
			if _, err := locker.Exec(t.Context(), `SELECT pg_advisory_unlock(1367001)`); err != nil {
				t.Fatal(err)
			}
			e, r := <-execution, <-reclaim
			if r.err != nil {
				t.Fatal(r.err)
			}
			if first == "execute" && (e.err != nil || e.result.Status != state.OperatorIntentSucceeded || r.count != 0) {
				t.Fatalf("execute winner: %+v %+v", e, r)
			}
			if first == "reclaim" {
				if r.count != 1 || !errors.Is(e.err, state.ErrOperatorNodeClaimLost) {
					t.Fatalf("reclaim winner: %+v %+v", e, r)
				}
				current, err := store.NodeGet(t.Context(), node.ID)
				if err != nil || current.Lifecycle != state.NodeLifecycleActive {
					t.Fatalf("obsolete worker acted: %+v %v", current, err)
				}
			}
		})
	}
}

// Lock order matches service_capacity_before_write even when protection is off.
// A maintenance transaction waiting for capacity must not already hold the node
// that the current capacity owner may need to update.
func TestOperatorNodePostgresCapacityLockOrder(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled_%t", enabled), func(t *testing.T) {
			store, pool := operatorNodePGStore(t)
			node, i := seedNodeIntent(t, store, state.OperatorIntentKindNodeDrain, state.NodeLifecycleActive)
			if _, err := store.SetServiceCapacityProtection(t.Context(), enabled); err != nil {
				t.Fatal(err)
			}
			conn, err := pool.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			tx, err := conn.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			if _, err := tx.Exec(t.Context(), `SELECT singleton FROM service_capacity_policy WHERE singleton FOR UPDATE`); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := store.ExecuteOperatorNode(ctx, i.ID, i.ClaimToken); done <- err }()
			waitOperatorNodeBlocked(t, pool, conn.Conn().PgConn().PID())
			var id string
			if err := tx.QueryRow(t.Context(), `SELECT id::text FROM compute_nodes WHERE id=$1 FOR UPDATE NOWAIT`, node.ID).Scan(&id); err != nil {
				t.Fatalf("maintenance locked node before capacity: %v", err)
			}
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

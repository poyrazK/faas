//go:build !no_pg

// adr: 1528
package state_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestNodeAdmissionPostgres(t *testing.T) {
	runNodeAdmissionTests(t, func(t *testing.T) nodeAdmissionTestStore { s, _ := lifecycleCompletionPGStore(t); return s })
}

func TestNodeVCPUUsagePostgres(t *testing.T) {
	s, _ := lifecycleCompletionPGStore(t)
	checkNodeVCPUUsage(t, s)
}

func TestNodeAdmissionPostgresRawWriterGuard(t *testing.T) {
	s, pool := lifecycleCompletionPGStore(t)
	app, dep := completionApplication(t, s)
	node := completionNode(t, s, state.NodeLifecycleActive)
	resident, err := s.CreateInstance(t.Context(), app, dep, "cold_booting", 128, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	parked, err := s.CreateInstance(t.Context(), app, dep, "parked", 128, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	node = completionTransition(t, s, node, state.NodeLifecycleMaintenance)
	for _, write := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO instances(app_id,deployment_id,node_id,state,ram_mb,wake_id) VALUES($1,$2,$3,'cold_booting',128,$4)`, []any{app, dep, node.ID, uuid.NewString()}},
		{`UPDATE instances SET state='waking' WHERE id=$1`, []any{parked.ID}},
	} {
		_, err := pool.Exec(t.Context(), write.sql, write.args...)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "instance_node_admission_current" {
			t.Fatalf("raw writer escaped node guard: %v", err)
		}
	}
	if _, err := pool.Exec(t.Context(), `UPDATE instances SET state='running' WHERE id=$1`, resident.ID); err != nil {
		t.Fatalf("existing reservation cannot finish: %v", err)
	}
	other := completionNode(t, s, state.NodeLifecycleActive)
	move, err := s.CreateInstance(t.Context(), app, dep, "migrating", 128, other.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE instances SET node_id=$2,state='running' WHERE id=$1`, move.ID, node.ID); err == nil {
		t.Fatal("raw migration entered maintenance node")
	}
}

func TestNodeAdmissionPostgresPolicyLockModes(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled_shared"
		if enabled {
			name = "enabled_exclusive"
		}
		t.Run(name, func(t *testing.T) {
			s, pool := lifecycleCompletionPGStore(t)
			app, dep := completionApplication(t, s)
			node := completionNode(t, s, state.NodeLifecycleActive)
			if _, err := pool.Exec(t.Context(), `UPDATE service_capacity_policy SET enabled=$1 WHERE singleton`, enabled); err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			if _, err := tx.Exec(t.Context(), `SELECT singleton FROM service_capacity_policy WHERE singleton FOR SHARE`); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := s.CreateInstance(selectedNodeContext(t.Context(), node), app, dep, "cold_booting", 128, node.ID, uuid.NewString())
				done <- err
			}()
			if enabled {
				waitLifecycleCompletionBlocked(t, pool, tx.Conn().PgConn().PID())
				if _, err := tx.Exec(t.Context(), `SELECT id FROM compute_nodes WHERE id=$1 FOR UPDATE NOWAIT`, node.ID); err != nil {
					t.Fatalf("admission locked node before capacity: %v", err)
				}
				if err := tx.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("disabled policy serialized unrelated admissions or enabled lock did not resume")
			}
		})
	}
}

func TestNodeAdmissionPostgresAdmissionBeforeDrain(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			s, pool := lifecycleCompletionPGStore(t)
			app, dep := completionApplication(t, s)
			node := completionNode(t, s, state.NodeLifecycleActive)
			if _, err := pool.Exec(t.Context(), `UPDATE service_capacity_policy SET enabled=$1 WHERE singleton`, enabled); err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			if _, err := tx.Exec(t.Context(), `INSERT INTO instances(app_id,deployment_id,node_id,state,ram_mb,wake_id) VALUES($1,$2,$3,'cold_booting',128,$4)`, app, dep, node.ID, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			drain := make(chan error, 1)
			go func() {
				drain <- s.NodeSetLifecycle(t.Context(), node.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining)
			}()
			waitLifecycleCompletionBlocked(t, pool, tx.Conn().PgConn().PID())
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := <-drain; err != nil {
				t.Fatal(err)
			}
			current := completionRead(t, s, node.ID)
			if _, err := s.CompleteNodeLifecycle(t.Context(), completionInput(current)); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("drain missed earlier admission: %v", err)
			}
			if _, err := s.CreateInstance(selectedNodeContext(t.Context(), node), app, dep, "cold_booting", 128, node.ID, uuid.NewString()); !errors.Is(err, state.ErrNodeAdmissionStale) {
				t.Fatalf("late admission entered draining node: %v", err)
			}
		})
	}
}

func TestNodeAdmissionPostgresCompletionBeforeQueuedAdmission(t *testing.T) {
	s, pool := lifecycleCompletionPGStore(t)
	app, dep := completionApplication(t, s)
	node := completionNode(t, s, state.NodeLifecycleActive)
	selected := selectedNodeContext(t.Context(), node)
	node = completionTransition(t, s, node, state.NodeLifecycleDraining)
	locker, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Release()
	if _, err := locker.Exec(t.Context(), `SELECT pg_advisory_lock(1369001)`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = locker.Exec(context.Background(), `SELECT pg_advisory_unlock(1369001)`) }()
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION admission_test_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(1369001); RETURN NEW; END $$;
CREATE TRIGGER zz_admission_test_barrier BEFORE UPDATE ON compute_nodes FOR EACH ROW WHEN (NEW.lifecycle='maintenance') EXECUTE FUNCTION admission_test_barrier()`); err != nil {
		t.Fatal(err)
	}
	complete := make(chan error, 1)
	go func() { _, err := s.CompleteNodeLifecycle(t.Context(), completionInput(node)); complete <- err }()
	waitLifecycleCompletionBlocked(t, pool, locker.Conn().PgConn().PID())
	var completionPID uint32
	if err := pool.QueryRow(t.Context(), `SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND $1::bigint=ANY(pg_blocking_pids(pid)::bigint[]) LIMIT 1`, locker.Conn().PgConn().PID()).Scan(&completionPID); err != nil {
		t.Fatal(err)
	}
	admit := make(chan error, 1)
	go func() {
		_, err := s.CreateInstance(selected, app, dep, "cold_booting", 128, node.ID, uuid.NewString())
		admit <- err
	}()
	waitLifecycleCompletionBlocked(t, pool, completionPID)
	if _, err := locker.Exec(t.Context(), `SELECT pg_advisory_unlock(1369001)`); err != nil {
		t.Fatal(err)
	}
	if err := <-complete; err != nil {
		t.Fatal(err)
	}
	if err := <-admit; !errors.Is(err, state.ErrNodeAdmissionStale) {
		t.Fatalf("queued admission crossed completed drain: %v", err)
	}
	current := completionRead(t, s, node.ID)
	rows, err := s.ListInstancesOnNodeID(t.Context(), node.ID)
	if err != nil || len(rows) != 0 || current.Lifecycle != state.NodeLifecycleMaintenance {
		t.Fatalf("completion raced a new resident: %+v %+v %v", current, rows, err)
	}
	current = completionTransition(t, s, current, state.NodeLifecycleActive)
	if _, err := s.CreateInstance(selected, app, dep, "cold_booting", 128, node.ID, uuid.NewString()); !errors.Is(err, state.ErrNodeAdmissionStale) {
		t.Fatalf("old selection accepted after activation: %v", err)
	}
	if _, err := s.CreateInstance(selectedNodeContext(t.Context(), current), app, dep, "cold_booting", 128, node.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
}

func TestNodeAdmissionPostgresAtomicInsertFailures(t *testing.T) {
	for _, fault := range []struct{ name, body, timing string }{
		{"failed", "RAISE EXCEPTION 'insert unavailable';", "BEFORE"},
		{"suppressed", "RETURN NULL;", "BEFORE"},
		{"commit", "RAISE EXCEPTION 'commit unavailable';", "AFTER"},
	} {
		t.Run(fault.name, func(t *testing.T) {
			s, pool := lifecycleCompletionPGStore(t)
			app, dep := completionApplication(t, s)
			node := completionNode(t, s, state.NodeLifecycleActive)
			ddl := "CREATE FUNCTION admission_insert_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN " + fault.body + " RETURN NEW; END $$; CREATE "
			if fault.timing == "AFTER" {
				ddl += "CONSTRAINT "
			}
			ddl += "TRIGGER admission_insert_fault " + fault.timing + " INSERT ON instances"
			if fault.timing == "AFTER" {
				ddl += " DEFERRABLE INITIALLY DEFERRED"
			}
			ddl += " FOR EACH ROW EXECUTE FUNCTION admission_insert_fault()"
			if _, err := pool.Exec(t.Context(), ddl); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateInstance(selectedNodeContext(t.Context(), node), app, dep, "cold_booting", 128, node.ID, uuid.NewString()); err == nil {
				t.Fatal("failed insert returned success")
			}
			rows, err := s.ListInstancesOnNodeID(t.Context(), node.ID)
			if err != nil || len(rows) != 0 {
				t.Fatalf("failed transaction left residency: %+v %v", rows, err)
			}
			if _, err := pool.Exec(t.Context(), `DROP TRIGGER admission_insert_fault ON instances`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateInstance(selectedNodeContext(t.Context(), node), app, dep, "cold_booting", 128, node.ID, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNodeAdmissionPostgresMigrationRoundTrip(t *testing.T) {
	body, err := os.ReadFile("../../migrations/20261017140000002_node_admission_fencing.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(body), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("missing down migration")
	}
	_, pool := lifecycleCompletionPGStore(t)
	if _, err := pool.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), parts[0]); err != nil {
		t.Fatal(err)
	}
}

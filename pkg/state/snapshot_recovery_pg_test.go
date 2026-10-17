//go:build !no_pg

// adr: 1523
package state_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func coldBootPGStore(t *testing.T) (*state.PgStore, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(pool), pool
}

func TestColdBootRecoveryPostgres(t *testing.T) {
	runColdBootStoreTests(t, func(t *testing.T) coldBootTestStore { s, _ := coldBootPGStore(t); return s })
}

func TestColdBootRecoveryPostgresAtomicFailures(t *testing.T) {
	for _, fault := range []struct{ name, table, event, body, trigger string }{
		{"generation_write", "deployments", "UPDATE", "RAISE EXCEPTION 'generation unavailable';", "BEFORE"},
		{"snapshot_write", "snapshots", "UPDATE", "RAISE EXCEPTION 'snapshot unavailable';", "BEFORE"},
		{"suppressed_snapshot_write", "snapshots", "UPDATE", "RETURN NULL;", "BEFORE"},
		{"audit_insert", "audit_event_outbox", "INSERT", "RAISE EXCEPTION 'audit unavailable';", "BEFORE"},
		{"suppressed_audit_insert", "audit_event_outbox", "INSERT", "RETURN NULL;", "BEFORE"},
		{"intent_write", "operator_intents", "UPDATE", "RAISE EXCEPTION 'intent unavailable';", "BEFORE"},
		{"suppressed_intent_write", "operator_intents", "UPDATE", "RETURN NULL;", "BEFORE"},
		{"commit", "operator_intents", "UPDATE", "RAISE EXCEPTION 'deferred failure';", "AFTER"},
	} {
		t.Run(fault.name, func(t *testing.T) {
			store, pool := coldBootPGStore(t)
			f := seedColdBoot(t, store)
			// Test-controlled DDL only: table/event/body come from the fixed cases above.
			ddl := "CREATE FUNCTION cold_boot_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN " + fault.body + " RETURN NEW; END $$; CREATE "
			if fault.trigger == "AFTER" {
				ddl += "CONSTRAINT "
			}
			ddl += "TRIGGER cold_boot_fault " + fault.trigger + " " + fault.event + " ON " + fault.table
			if fault.trigger == "AFTER" {
				ddl += " DEFERRABLE INITIALLY DEFERRED"
			}
			ddl += " FOR EACH ROW EXECUTE FUNCTION cold_boot_fault()"
			if _, err := pool.Exec(t.Context(), ddl); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ExecuteOperatorColdBoot(t.Context(), f.intent.ID, f.intent.ClaimToken); err == nil {
				t.Fatal("fault reported success")
			}
			row, err := store.GetOperatorIntent(t.Context(), f.intent.ID)
			if err != nil || row.Status != state.OperatorIntentRunning || row.FinishedAt != nil || row.ColdBootGeneration != 0 || row.CompletionAuditOutboxID != 0 {
				t.Fatalf("partial completion: %+v, %v", row, err)
			}
			var generation, audits, stale int64
			if err := pool.QueryRow(t.Context(), `SELECT snapshot_recovery_generation,
 (SELECT count(*) FROM audit_event_outbox), (SELECT count(*) FROM snapshots WHERE deployment_id=$1 AND stale)
 FROM deployments WHERE id=$1`, f.dep.ID).Scan(&generation, &audits, &stale); err != nil {
				t.Fatal(err)
			}
			if generation != 0 || audits != 0 || stale != 0 {
				t.Fatalf("partial recovery: generation=%d audits=%d stale=%d", generation, audits, stale)
			}
			if _, err := pool.Exec(t.Context(), "DROP TRIGGER cold_boot_fault ON "+fault.table); err != nil {
				t.Fatal(err)
			}
			if r := f.execute(t); r.Generation != 1 || !slices.Equal(r.SnapshotIDs, f.snapshots) {
				t.Fatalf("retry after rollback: %+v", r)
			}
		})
	}
}

// The advisory lock holds the winner inside a real production transaction.
// pg_blocking_pids establishes ordering before the competing operation starts.
func waitColdBootBlocked(t *testing.T, pool *pgxpool.Pool, blocker uint32) {
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
			t.Fatal("operation never reached serialization barrier")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestColdBootRecoveryPostgresPublicationSerialization(t *testing.T) {
	for _, first := range []string{"publication", "recovery"} {
		t.Run(first+"_first", func(t *testing.T) {
			store, pool := coldBootPGStore(t)
			f := seedColdBoot(t, store)
			source := f.source(t, "")
			if err := store.MarkSnapshotStale(t.Context(), f.snapshots[0]); err != nil {
				t.Fatal(err)
			}
			locker, err := pool.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer locker.Release()
			if _, err := locker.Exec(t.Context(), `SELECT pg_advisory_lock(1364001)`); err != nil {
				t.Fatal(err)
			}
			defer func() { _, _ = locker.Exec(context.Background(), `SELECT pg_advisory_unlock(1364001)`) }()
			table, event := "snapshots", "INSERT"
			if first == "recovery" {
				table, event = "deployments", "UPDATE"
			}
			if _, err := pool.Exec(t.Context(), `CREATE FUNCTION cold_boot_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(1364001); RETURN NEW; END $$; CREATE TRIGGER cold_boot_barrier BEFORE `+event+` ON `+table+` FOR EACH ROW EXECUTE FUNCTION cold_boot_barrier()`); err != nil {
				t.Fatal(err)
			}
			type publicationResult struct {
				snap state.Snapshot
				err  error
			}
			type recoveryResult struct {
				result state.OperatorColdBootResult
				err    error
			}
			published := make(chan publicationResult, 1)
			recovered := make(chan recoveryResult, 1)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			publish := func() {
				snap, err := store.PublishSnapshotIfRuntimeFresh(ctx, f.snapshot(state.SnapshotTierWarm), source.ID, source.StartedAt)
				published <- publicationResult{snap, err}
			}
			recoverCache := func() {
				result, err := store.ExecuteOperatorColdBoot(ctx, f.intent.ID, f.intent.ClaimToken)
				recovered <- recoveryResult{result, err}
			}
			if first == "publication" {
				go publish()
			} else {
				go recoverCache()
			}
			waitColdBootBlocked(t, pool, locker.Conn().PgConn().PID())
			if first == "publication" {
				go recoverCache()
			} else {
				go publish()
			}
			// A blocked first transaction holds the parent app lock. The second
			// operation therefore cannot publish an inconsistent intermediate state.
			if _, err := locker.Exec(t.Context(), `SELECT pg_advisory_unlock(1364001)`); err != nil {
				t.Fatal(err)
			}
			p, r := <-published, <-recovered
			if r.err != nil || r.result.Generation != 1 {
				t.Fatalf("recovery: %+v, %v", r.result, r.err)
			}
			if first == "publication" {
				if p.err != nil || !slices.Contains(r.result.SnapshotIDs, p.snap.ID) {
					t.Fatalf("committed publication escaped invalidation: %+v, %v, %+v", p.snap, p.err, r.result)
				}
			} else if !errors.Is(p.err, state.ErrSnapshotRuntimeStale) {
				t.Fatalf("late publication succeeded: %+v, %v", p.snap, p.err)
			}
			if _, err := store.LatestSnapshot(t.Context(), f.dep.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("usable cache survived recovery: %v", err)
			}
		})
	}
}

func TestColdBootRecoveryPostgresMigrationDown(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../migrations/20261017100000001_operator_cold_boot_recovery.sql"))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	store, pool := coldBootPGStore(t)
	// It is reversible before use, and refuses to erase an active boundary.
	if _, err := pool.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), parts[0]); err != nil {
		t.Fatal(err)
	}
	f := seedColdBoot(t, store)
	f.execute(t)
	if _, err := pool.Exec(t.Context(), parts[1]); err == nil {
		t.Fatal("downgrade erased a durable recovery boundary")
	}
}

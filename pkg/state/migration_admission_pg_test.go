//go:build !no_pg

// adr: 1529
package state_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrationAdmissionPostgres(t *testing.T) {
	runMigrationAdmissionTests(t, func(t *testing.T) migrationAdmissionStore { s, _ := lifecycleCompletionPGStore(t); return s })
}

func migrationAdmissionBarrier(t *testing.T, pool *pgxpool.Pool, table, event string) *pgxpool.Conn {
	t.Helper()
	locker, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(locker.Release)
	if _, err := locker.Exec(t.Context(), `SELECT pg_advisory_lock(1370001)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = locker.Exec(context.Background(), `SELECT pg_advisory_unlock(1370001)`) })
	ddl := `CREATE FUNCTION incoming_test_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(1370001); RETURN NEW; END $$; CREATE TRIGGER zz_incoming_test_barrier BEFORE ` + event + ` ON ` + table + ` FOR EACH ROW EXECUTE FUNCTION incoming_test_barrier()`
	if _, err := pool.Exec(t.Context(), ddl); err != nil {
		t.Fatal(err)
	}
	return locker
}

func TestMigrationAdmissionPostgresSerializesDrainAndSourceResume(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "policy_off", true: "policy_on"}[enabled], func(t *testing.T) {
			s, pool := lifecycleCompletionPGStore(t)
			f := newMigrationAdmissionFixture(t, s)
			if _, err := s.SetServiceCapacityProtection(t.Context(), enabled); err != nil {
				t.Fatal(err)
			}
			locker := migrationAdmissionBarrier(t, pool, "migration_admissions", "INSERT")
			reserved := make(chan error, 1)
			go func() { _, err := s.ReserveMigrationAdmission(t.Context(), f.plan); reserved <- err }()
			waitLifecycleCompletionBlocked(t, pool, locker.Conn().PgConn().PID())
			var reservingPID uint32
			if err := pool.QueryRow(t.Context(), `SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND $1::bigint=ANY(pg_blocking_pids(pid)::bigint[]) LIMIT 1`, locker.Conn().PgConn().PID()).Scan(&reservingPID); err != nil {
				t.Fatal(err)
			}
			resumed := make(chan bool, 1)
			errResume := make(chan error, 1)
			go func() {
				safe, err := s.FenceMigrationSourceResume(t.Context(), f.instance.ID, f.plan.LeaseToken)
				resumed <- safe
				errResume <- err
			}()
			waitLifecycleCompletionBlocked(t, pool, reservingPID)
			drained := make(chan error, 1)
			go func() {
				drained <- s.NodeSetLifecycle(t.Context(), f.dest.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining)
			}()
			if _, err := locker.Exec(t.Context(), `SELECT pg_advisory_unlock(1370001)`); err != nil {
				t.Fatal(err)
			}
			if err := <-reserved; err != nil {
				t.Fatal(err)
			}
			if safe, err := <-resumed, <-errResume; safe || err != nil {
				t.Fatalf("source crossed reservation: %t %v", safe, err)
			}
			if err := <-drained; err != nil {
				t.Fatal(err)
			}
			n := completionRead(t, s, f.dest.ID)
			if _, err := s.CompleteNodeLifecycle(t.Context(), completionInput(n)); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("drain passed pending arrival: %v", err)
			}
		})
	}
	t.Run("source_resume_wins", func(t *testing.T) {
		s, pool := lifecycleCompletionPGStore(t)
		f := newMigrationAdmissionFixture(t, s)
		locker := migrationAdmissionBarrier(t, pool, "migration_leases", "UPDATE")
		resumed := make(chan error, 1)
		go func() {
			safe, err := s.FenceMigrationSourceResume(t.Context(), f.instance.ID, f.plan.LeaseToken)
			if err == nil && !safe {
				err = errors.New("source resume refused")
			}
			resumed <- err
		}()
		waitLifecycleCompletionBlocked(t, pool, locker.Conn().PgConn().PID())
		reserved := make(chan error, 1)
		go func() { _, err := s.ReserveMigrationAdmission(t.Context(), f.plan); reserved <- err }()
		if _, err := locker.Exec(t.Context(), `SELECT pg_advisory_unlock(1370001)`); err != nil {
			t.Fatal(err)
		}
		if err := <-resumed; err != nil {
			t.Fatal(err)
		}
		if err := <-reserved; !errors.Is(err, state.ErrConflict) {
			t.Fatalf("delayed reserve passed source resume: %v", err)
		}
	})
}

func TestMigrationAdmissionPostgresCommitFaultsAreAtomic(t *testing.T) {
	for _, fault := range []struct{ name, body, timing string }{
		{"failed_receipt", "RAISE EXCEPTION 'receipt unavailable';", "BEFORE"},
		{"suppressed_receipt", "RETURN NULL;", "BEFORE"},
		{"deferred_commit", "RAISE EXCEPTION 'deferred receipt failure';", "AFTER"},
	} {
		t.Run(fault.name, func(t *testing.T) {
			s, pool := lifecycleCompletionPGStore(t)
			f := newMigrationAdmissionFixture(t, s)
			f.reserve(t)
			f.adopt(t)
			ddl := `CREATE FUNCTION incoming_commit_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN ` + fault.body + ` RETURN NEW; END $$; CREATE `
			if fault.timing == "AFTER" {
				ddl += "CONSTRAINT "
			}
			ddl += `TRIGGER incoming_commit_fault ` + fault.timing + ` UPDATE ON migration_admissions`
			if fault.timing == "AFTER" {
				ddl += ` DEFERRABLE INITIALLY DEFERRED`
			}
			ddl += ` FOR EACH ROW WHEN (NEW.phase='committed') EXECUTE FUNCTION incoming_commit_fault()`
			if _, err := pool.Exec(t.Context(), ddl); err != nil {
				t.Fatal(err)
			}
			if err := f.commit(t.Context()); err == nil {
				t.Fatal("failed ownership transaction reported success")
			}
			i, err := s.InstanceByID(t.Context(), f.instance.ID)
			if err != nil || i.NodeID != f.source.ID || i.State != "migrating" {
				t.Fatal("ownership partially committed", i, err)
			}
			a, err := s.MigrationAdmissionByID(t.Context(), f.plan.ID)
			if err != nil || a.Phase != state.MigrationAdopted {
				t.Fatal("reservation partially consumed", a, err)
			}
			if _, err := pool.Exec(t.Context(), `DROP TRIGGER incoming_commit_fault ON migration_admissions`); err != nil {
				t.Fatal(err)
			}
			if err := f.commit(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrationAdmissionPostgresCommitAndCleanupSerialize(t *testing.T) {
	for _, first := range []string{"ownership", "cleanup", "cleanup_before_direct_writer"} {
		t.Run(first, func(t *testing.T) {
			s, pool := lifecycleCompletionPGStore(t)
			f := newMigrationAdmissionFixture(t, s)
			f.reserve(t)
			f.adopt(t)
			locker := migrationAdmissionBarrier(t, pool, "migration_admissions", "UPDATE")
			committed := make(chan error, 1)
			cleaned := make(chan state.MigrationAdmission, 1)
			cleanupErr := make(chan error, 1)
			commit := func() {
				if first == "cleanup_before_direct_writer" {
					_, err := pool.Exec(t.Context(), `UPDATE instances SET node_id=$1,wake_id=$2,state='running' WHERE id=$3`, f.dest.ID, f.plan.DestinationWakeID, f.instance.ID)
					committed <- err
				} else {
					committed <- f.commit(t.Context())
				}
			}
			cleanup := func() {
				a, err := s.RequestMigrationAdmissionCleanup(t.Context(), f.plan.ID, f.dest.ID)
				cleaned <- a
				cleanupErr <- err
			}
			if first == "ownership" {
				go commit()
			} else {
				go cleanup()
			}
			waitLifecycleCompletionBlocked(t, pool, locker.Conn().PgConn().PID())
			var holdingPID uint32
			if err := pool.QueryRow(t.Context(), `SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND $1::bigint=ANY(pg_blocking_pids(pid)::bigint[]) LIMIT 1`, locker.Conn().PgConn().PID()).Scan(&holdingPID); err != nil {
				t.Fatal(err)
			}
			if first == "ownership" {
				go cleanup()
			} else {
				go commit()
			}
			waitLifecycleCompletionBlocked(t, pool, holdingPID)
			if _, err := locker.Exec(t.Context(), `SELECT pg_advisory_unlock(1370001)`); err != nil {
				t.Fatal(err)
			}
			commitErr := <-committed
			a, err := <-cleaned, <-cleanupErr
			if err != nil {
				t.Fatal(err)
			}
			if first == "ownership" {
				if commitErr != nil || a.Phase != state.MigrationCommitted {
					t.Fatal("cleanup revoked committed ownership", a, commitErr)
				}
			} else {
				if commitErr == nil || a.Phase != state.MigrationCleaning {
					t.Fatal("late ownership crossed cleanup fence", a, commitErr)
				}
				i, err := s.InstanceByID(t.Context(), f.instance.ID)
				if err != nil || i.NodeID != f.source.ID || i.State != "migrating" {
					t.Fatal("late ownership partially published", i, err)
				}
			}
		})
	}
}

func TestMigrationAdmissionPostgresDirectWriteGuards(t *testing.T) {
	s, pool := lifecycleCompletionPGStore(t)
	f := newMigrationAdmissionFixture(t, s)
	f.reserve(t)
	for _, sql := range []string{
		`DELETE FROM migration_admissions WHERE id=$1`,
		`UPDATE migration_admissions SET ram_mb=ram_mb+1 WHERE id=$1`,
		`UPDATE migration_admissions SET phase='committed',adoption_receipt='fake' WHERE id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), sql, f.plan.ID); err == nil {
			t.Fatalf("unguarded admission mutation: %s", sql)
		}
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM migration_leases WHERE lease_token=$1`, f.plan.LeaseToken); err == nil {
		t.Fatal("pending arrival lost source lease")
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM compute_nodes WHERE id=$1`, f.dest.ID); err == nil {
		t.Fatal("pending destination was deleted")
	}
	f.adopt(t)
	if _, err := pool.Exec(t.Context(), `UPDATE instances SET node_id=$1,wake_id=$2,state='running',lease_token=NULL WHERE id=$3`, f.dest.ID, f.plan.DestinationWakeID, f.instance.ID); err == nil {
		t.Fatal("direct owner discarded original migration lease")
	}
	if _, err := s.RequestMigrationAdmissionCleanup(t.Context(), f.plan.ID, f.dest.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE instances SET node_id=$1,wake_id=$2,state='running' WHERE id=$3`, f.dest.ID, f.plan.DestinationWakeID, f.instance.ID); err == nil {
		t.Fatal("direct owner update crossed cleanup fence")
	}
}

func TestMigrationAdmissionPostgresMigrationRoundTrip(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../migrations/20261017140000003_inbound_migration_reservations.sql"))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(body), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("missing down migration")
	}
	s, pool := lifecycleCompletionPGStore(t)
	if _, err := pool.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), parts[0]); err != nil {
		t.Fatal(err)
	}
	f := newMigrationAdmissionFixture(t, s)
	f.reserve(t)
	if _, err := pool.Exec(t.Context(), parts[1]); err == nil {
		t.Fatal("downgrade discarded admission evidence")
	}
}

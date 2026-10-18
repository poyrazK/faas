//go:build !no_pg

// adr: 1530
package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrationSourcePostgres(t *testing.T) {
	runMigrationSourceTests(t, func(t *testing.T) migrationAdmissionStore { s, _ := lifecycleCompletionPGStore(t); return s })
}

func TestMigrationSourcePostgresAtomicCommitFailures(t *testing.T) {
	for _, fault := range []struct{ name, body, timing string }{
		{"write_error", "RAISE EXCEPTION 'source retirement unavailable';", "BEFORE"},
		{"suppressed_write", "RETURN NULL;", "BEFORE"},
		{"deferred_error", "RAISE EXCEPTION 'source commit reply failed';", "AFTER"},
	} {
		t.Run(fault.name, func(t *testing.T) {
			s, pool := lifecycleCompletionPGStore(t)
			f := newMigrationAdmissionFixture(t, s)
			f.reserve(t)
			f.adopt(t)
			ddl := "CREATE FUNCTION source_retirement_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN " + fault.body + " RETURN NEW; END $$; CREATE "
			if fault.timing == "AFTER" {
				ddl += "CONSTRAINT "
			}
			ddl += "TRIGGER zz_source_retirement_fault " + fault.timing + " UPDATE ON migration_sources"
			if fault.timing == "AFTER" {
				ddl += " DEFERRABLE INITIALLY DEFERRED"
			}
			ddl += " FOR EACH ROW EXECUTE FUNCTION source_retirement_fault()"
			if _, err := pool.Exec(t.Context(), ddl); err != nil {
				t.Fatal(err)
			}
			if err := f.commit(t.Context()); err == nil {
				t.Fatal("ownership committed without source retirement")
			}
			i, err := s.MigrationInstanceByID(t.Context(), f.instance.ID)
			if err != nil || i.NodeID != f.source.ID || i.WakeID != f.instance.WakeID {
				t.Fatalf("partial ownership: %+v %v", i, err)
			}
			if r := sourceRecord(t, f); r.Phase != state.MigrationSourceBound {
				t.Fatalf("partial retirement: %+v", r)
			}
			a, err := s.MigrationAdmissionByID(t.Context(), f.plan.ID)
			if err != nil || a.Phase != state.MigrationAdopted {
				t.Fatalf("partial incoming receipt: %+v %v", a, err)
			}
			if _, err := pool.Exec(t.Context(), "DROP TRIGGER zz_source_retirement_fault ON migration_sources"); err != nil {
				t.Fatal(err)
			}
			if err := f.commit(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrationSourcePostgresCommitDrainRace(t *testing.T) {
	s, pool := lifecycleCompletionPGStore(t)
	f := newMigrationAdmissionFixture(t, s)
	f.reserve(t)
	f.adopt(t)
	n := completionTransition(t, s, f.source, state.NodeLifecycleDraining)
	locker := migrationAdmissionBarrier(t, pool, "migration_sources", "UPDATE")
	committed := make(chan error, 1)
	go func() { committed <- f.commit(t.Context()) }()
	waitLifecycleCompletionBlocked(t, pool, locker.Conn().PgConn().PID())
	completed := make(chan error, 1)
	go func() { _, err := s.CompleteNodeLifecycle(t.Context(), completionInput(n)); completed <- err }()
	if _, err := locker.Exec(t.Context(), `SELECT pg_advisory_unlock(1370001)`); err != nil {
		t.Fatal(err)
	}
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
	if err := <-completed; !errors.Is(err, state.ErrConflict) {
		t.Fatalf("maintenance crossed ownership/retirement: %v", err)
	}
	if got := completionRead(t, s, n.ID); got.Lifecycle != state.NodeLifecycleDraining {
		t.Fatalf("source entered maintenance: %+v", got)
	}
}

func TestMigrationSourcePostgresRawWritersCannotEraseOrBypass(t *testing.T) {
	s, pool := lifecycleCompletionPGStore(t)
	f := newMigrationAdmissionFixture(t, s)
	for _, query := range []string{
		`DELETE FROM migration_sources WHERE lease_token=$1`,
		`UPDATE migration_sources SET physical_binding=repeat('f',64) WHERE lease_token=$1`,
		`DELETE FROM migration_leases WHERE lease_token=$1`,
		`UPDATE migration_sources SET phase='resumed',proof='unfenced' WHERE lease_token=$1`,
	} {
		if _, err := pool.Exec(t.Context(), query, f.plan.LeaseToken); err == nil {
			t.Fatalf("raw writer bypassed source: %s", query)
		}
	}
	f.reserve(t)
	f.adopt(t)
	if err := f.commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`UPDATE compute_nodes SET lifecycle='maintenance' WHERE id=$1`, `UPDATE compute_nodes SET lifecycle='retired' WHERE id=$1`, `DELETE FROM compute_nodes WHERE id=$1`} {
		if _, err := pool.Exec(t.Context(), query, f.source.ID); err == nil {
			t.Fatalf("raw maintenance bypassed source: %s", query)
		}
	}
}

func TestMigrationSourcePostgresBoundResourcesSurviveRowAndAppChanges(t *testing.T) {
	s, pool := lifecycleCompletionPGStore(t)
	f := newMigrationAdmissionFixture(t, s)
	r := sourceRecord(t, f)
	if _, err := pool.Exec(t.Context(), `UPDATE instances SET state='running',capacity_ram_mb=1,capacity_cpu_millicores=1,capacity_vcpu=1 WHERE id=$1`, f.instance.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE apps SET cpu_millicores=250 WHERE id=$1`, f.instance.AppID); err != nil {
		t.Fatal(err)
	}
	used, err := s.ComputeNodeUsedMB(t.Context(), f.source.ID)
	if err != nil || used < int64(r.RAMMB) {
		t.Fatalf("bound physical RAM disappeared: %d %v", used, err)
	}
	cpu, err := s.ComputeNodeUsedCPUMillicoresByNode(t.Context(), []string{f.source.ID})
	if err != nil || cpu[f.source.ID] < int64(r.CPUMillicores) {
		t.Fatalf("bound physical CPU disappeared: %v %v", cpu, err)
	}
}

func TestMigrationSourcePostgresTerminalLeaseCannotBeReplacedBeforeCleanup(t *testing.T) {
	s, pool := lifecycleCompletionPGStore(t)
	f := newMigrationAdmissionFixture(t, s)
	f.reserve(t)
	f.adopt(t)
	if err := f.commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	r := sourceRecord(t, f)
	if err := s.AcknowledgeMigrationSource(t.Context(), r.MigrationSourceBinding, state.MigrationSourceRetired, "original-retired"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE migration_leases SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE lease_token=$1`, r.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE migration_leases SET lease_token=lease_token||'-replacement',mem_storage_key='' WHERE lease_token=$1`, r.LeaseToken); err == nil {
		t.Fatal("terminal snapshot cleanup lost its only durable handles")
	}
}

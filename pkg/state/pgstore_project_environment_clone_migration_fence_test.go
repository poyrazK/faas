//go:build !no_pg

// adr: 590
package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func requireCloneMigrationRetained(t *testing.T, err error) {
	t.Helper()
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.Code != "23514" || e.ConstraintName != "clone_checkpoint_migration_retained" {
		t.Fatalf("want retained migration ownership, got %v", err)
	}
}

func TestPgCloneMigrationFenceRetainsOriginalLeaseAndInstance(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "live"
		if expired {
			name = "expired"
		}
		t.Run(name, func(t *testing.T) {
			s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
			ctx := t.Context()
			in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			w := in.Plan.Workloads[0]
			node := resolveDefaultLocal(t, ctx, s)
			ins, err := s.CreateInstance(ctx, w.AppID, w.TargetDeploymentID, string(state.StateRunning), 128, node, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			until := time.Now().UTC().Add(time.Minute)
			if expired {
				until = time.Now().UTC().Add(-time.Minute)
			}
			lease := state.MigrationLease{InstanceID: ins.ID, LeaseToken: uuid.NewString(), SourceNodeID: node, CreatedAt: time.Now().UTC().Add(-2 * time.Minute), LeaseExpiresAt: until}
			if err = s.ReserveMigrationLease(ctx, lease); err != nil {
				t.Fatal(err)
			}
			in, _, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			requireCloneMigrationRetained(t, s.DeleteMigrationLease(ctx, lease.LeaseToken))
			_, err = pool.Exec(ctx, "DELETE FROM instances WHERE id=$1", ins.ID)
			requireCloneMigrationRetained(t, err)
			_, err = pool.Exec(ctx, "DELETE FROM deployments WHERE id=$1", w.TargetDeploymentID)
			requireCloneMigrationRetained(t, err)
			// Migration admissions and sources reference leases (ADR-1529/1530),
			// so a plain TRUNCATE stops at their foreign keys. CASCADE reaches
			// the lease table's own retention trigger, which must still refuse.
			_, err = pool.Exec(ctx, "TRUNCATE migration_leases CASCADE")
			requireCloneMigrationRetained(t, err)
			for _, q := range []string{
				"UPDATE migration_leases SET lease_expires_at=clock_timestamp()+interval '1 minute' WHERE instance_id=$1",
				"UPDATE migration_leases SET lease_token='replacement' WHERE instance_id=$1",
				"UPDATE migration_leases SET source_node_id='replacement' WHERE instance_id=$1",
			} {
				_, err = pool.Exec(ctx, q, ins.ID)
				requireCloneSchedulerFence(t, err)
			}
			if expired {
				replacement := lease
				replacement.LeaseToken = uuid.NewString()
				replacement.LeaseExpiresAt = time.Now().UTC().Add(time.Minute)
				requireCloneSchedulerFence(t, s.ReserveMigrationLease(ctx, replacement))
			} else {
				requireCloneSchedulerFence(t, s.RenewMigrationLease(ctx, lease.LeaseToken, node, time.Now().UTC().Add(2*time.Minute)))
			}
			// A pre-fence snapshot's delayed completion may add original handles for
			// cleanup, but cannot grant a fresh handoff or overwrite completed handles.
			if err = s.CompleteMigrationLease(ctx, lease.LeaseToken, "original-mem", "original-state"); err != nil {
				t.Fatal("lost completion cannot be recorded", err)
			}
			if err = s.CompleteMigrationLease(ctx, lease.LeaseToken, "original-mem", "original-state"); err != nil {
				t.Fatal("completion replay", err)
			}
			requireCloneSchedulerFence(t, s.CompleteMigrationLease(ctx, lease.LeaseToken, "foreign-mem", "foreign-state"))
			saved, err := s.GetMigrationLease(ctx, ins.ID, lease.LeaseToken)
			if err != nil || saved.Pending || saved.SourceNodeID != node || saved.MemStorageKey != "original-mem" || saved.VMStateStorageKey != "original-state" {
				t.Fatal("original evidence lost", saved, err)
			}
			if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, pgWorkloadRetirementProof(in.Plan)); !errors.Is(err, state.ErrConflict) {
				t.Fatal("unresolved handoff accepted", err)
			}
			native, err := s.ProjectEnvironmentCloneCheckpointWorkloadRetirementForLease(ctx, l)
			if err != nil || native.State != "requested" {
				t.Fatal("partial retirement receipt", native, err)
			}
			// Source work does not acquire this clone's fence.
			source, err := s.CreateInstance(ctx, w.AppID, w.SourceDeploymentID, string(state.StateRunning), 128, node, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			other := lease
			other.InstanceID = source.ID
			other.LeaseToken = uuid.NewString()
			other.LeaseExpiresAt = time.Now().UTC().Add(time.Minute)
			if err = s.ReserveMigrationLease(ctx, other); err != nil {
				t.Fatal("source reserve", err)
			}
			if err = s.RenewMigrationLease(ctx, other.LeaseToken, node, time.Now().UTC().Add(2*time.Minute)); err != nil {
				t.Fatal("source renew", err)
			}
			if err = s.CompleteMigrationLease(ctx, other.LeaseToken, "source-mem", "source-state"); err != nil {
				t.Fatal(err)
			}
			if err = s.DeleteMigrationLease(ctx, other.LeaseToken); err != nil {
				t.Fatal("source release", err)
			}
		})
	}
}

func TestPgCloneMigrationFenceQueuedAdmissionAndOldSnapshot(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
	ctx := t.Context()
	in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	w := in.Plan.Workloads[0]
	node := resolveDefaultLocal(t, ctx, s)
	ins, err := s.CreateInstance(ctx, w.AppID, w.TargetDeploymentID, string(state.StateRunning), 128, node, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	lease := state.MigrationLease{InstanceID: ins.ID, LeaseToken: uuid.NewString(), SourceNodeID: node, CreatedAt: time.Now().UTC(), LeaseExpiresAt: time.Now().UTC().Add(time.Minute)}
	old, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Rollback(ctx) }()
	if _, err = old.Exec(ctx, "SELECT state FROM project_environment_clone_checkpoint_workload_retirements"); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT lock_clone_checkpoint_scheduler_admission()"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.ReserveMigrationLease(ctx, lease) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%insert into migration_leases%')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("migration did not queue")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.Exec(ctx, "UPDATE project_environment_clone_checkpoint_workload_retirements SET state='requested',requested_at=clock_timestamp() WHERE operation_id=$1", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		requireCloneSchedulerFence(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("migration remained queued")
	}
	_, err = old.Exec(ctx, "INSERT INTO migration_leases(instance_id,lease_token,source_node_id,created_at,lease_expires_at) VALUES($1,'stale',$2,clock_timestamp(),clock_timestamp()+interval '1 minute')", ins.ID, node)
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.Code != "40001" {
		t.Fatal("old snapshot admitted migration", err)
	}
}

func TestPgCloneMigrationFenceEmptyMigrationAndRetention(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "unmaterialized")
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261011232006423_clone_migration_lease_fences.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, q := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(ctx, q); err != nil {
			t.Fatal("empty migration replay", err)
		}
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, parts[1])
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.ConstraintName != "clone_checkpoint_migration_down_no_evidence" {
		t.Fatal("unsafe downgrade", err)
	}
}

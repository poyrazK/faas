//go:build !no_pg

// adr: 569
package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func cloneForkCompensation(t *testing.T, s *state.PgStore, ctx context.Context, lease state.ProjectEnvironmentCloneLease) state.ProjectEnvironmentCloneLease {
	t.Helper()
	op := lease.Operation
	var err error
	lease.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestPgClonePostgresSnapshotRestoreCleanupUndispatchedQuotaAndSourceHold(t *testing.T) {
	s, ctx, pool, lease, snapshot := cloneSnapshotRestoreFixture(t)
	receipt, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("capture allowed retirement: %v", err)
	}
	lease = cloneForkCompensation(t, s, ctx, lease)
	proof := state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{Done: true}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unbegun retirement: %v", err)
	}
	receipt, err = s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID)
	if err != nil || receipt.State != "deleting" || !receipt.RequestStartedAt.IsZero() {
		t.Fatalf("local cleanup intent: %+v %v", receipt, err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(ctx, lease, snapshot.SourceDatabaseID, cloneSnapshotRestoreObservation(snapshot)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("undispatched reservation acquired remote identity: %v", err)
	}
	databases, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	input := managedpostgres.Database{ID: uuid.NewString(), AccountID: lease.Operation.AccountID, Name: "replacement", State: managedpostgres.StateProvisioning,
		BackendID: receipt.BackendID, BackendFingerprint: strings.Repeat("a", 64), DesiredGeneration: 1,
		Spec: managedpostgres.Spec{Region: "eu-central-1", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if _, _, err := databases.Reserve(ctx, input, 2); !errors.Is(err, managedpostgres.ErrQuotaExceeded) {
		t.Fatalf("deleting reservation released quota: %v", err)
	}
	receipt, err = s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID, proof)
	if err != nil || receipt.State != "deleted" || receipt.DeletedAt.IsZero() || receipt.TargetProviderResourceID != "" {
		t.Fatalf("local retirement: %+v %v", receipt, err)
	}
	if _, _, err := databases.Reserve(ctx, input, 2); err != nil {
		t.Fatalf("verified local retirement kept quota: %v", err)
	}
	op := lease.Operation
	retiredResources := append([]state.ProjectEnvironmentCloneResource{}, op.Resources...)
	for i := range retiredResources {
		retiredResources[i].Status = "compensated"
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensated, op.Revision, retiredResources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("retained source snapshot lost recovery authority: %v", err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	if replay, err := s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID, proof); err != nil || replay != receipt {
		t.Fatalf("deleted replay after source retirement: %+v %v", replay, err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensated, op.Revision, retiredResources, ""); err != nil {
		t.Fatalf("verified retirement could not terminate: %v", err)
	}
}

func TestPgClonePostgresSnapshotRestoreCleanupPinsRecoveredIdentityAndOperations(t *testing.T) {
	s, ctx, _, lease, snapshot := cloneSnapshotRestoreFixture(t)
	if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	lease = cloneForkCompensation(t, s, ctx, lease)
	receipt, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID)
	if err != nil || receipt.TargetProviderResourceID != "" {
		t.Fatalf("unknown target: %+v %v", receipt, err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID, state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{Done: true}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("absence retired unknown dispatch: %v", err)
	}
	observation := cloneSnapshotRestoreObservation(snapshot)
	receipt, err = s.RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(ctx, lease, snapshot.SourceDatabaseID, observation)
	if err != nil || receipt.State != "deleting" || receipt.TargetProviderResourceID != observation.TargetProviderResourceID || receipt.ObservedAt.IsZero() {
		t.Fatalf("recovered identity: %+v %v", receipt, err)
	}
	changed := observation
	changed.TargetCreatedAt = changed.TargetCreatedAt.Add(time.Microsecond)
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(ctx, lease, snapshot.SourceDatabaseID, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replaced recovered creation time: %v", err)
	}
	proof := state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{TargetProviderResourceID: receipt.TargetProviderResourceID, OperationIDs: []string{"delete-b", "delete-a"}, Done: true}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unpinned operation set retired fork: %v", err)
	}
	receipt, err = s.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(ctx, lease, snapshot.SourceDatabaseID, proof)
	if err != nil || receipt.State != "deleting" || !receipt.DeletedAt.IsZero() || receipt.DeletionOperations != `["delete-a", "delete-b"]` {
		t.Fatalf("operation intent: %+v %v", receipt, err)
	}
	for _, fault := range []string{"foreign_target", "changed_operations", "duplicate_operations", "pending", "missing_operations"} {
		t.Run(fault, func(t *testing.T) {
			bad := proof
			bad.OperationIDs = append([]string{}, proof.OperationIDs...)
			switch fault {
			case "foreign_target":
				bad.TargetProviderResourceID += "other"
			case "changed_operations":
				bad.OperationIDs[0] += "other"
			case "duplicate_operations":
				bad.OperationIDs = []string{"delete-a", "delete-a"}
			case "pending":
				bad.Done = false
			case "missing_operations":
				bad.OperationIDs = nil
			}
			if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID, bad); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("accepted %s: %v", fault, err)
			}
		})
	}
	old := lease
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	lease, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, old, snapshot.SourceDatabaseID, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old worker retired fork: %v", err)
	}
	if replay, err := s.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, snapshot.SourceDatabaseID); err != nil || replay != receipt {
		t.Fatalf("handoff changed recovery receipt: %+v %v", replay, err)
	}
	receipt, err = s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID, proof)
	if err != nil || receipt.State != "deleted" || receipt.DeletedAt.IsZero() {
		t.Fatalf("verified remote retirement: %+v %v", receipt, err)
	}
	if replay, err := s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID, proof); err != nil || replay != receipt {
		t.Fatalf("terminal replay changed receipt: %+v %v", replay, err)
	}
}

func TestPgClonePostgresSnapshotRestoreCleanupChecksLeaseAfterForkLock(t *testing.T) {
	for _, action := range []string{"begin", "identity", "operations", "finish"} {
		t.Run(action, func(t *testing.T) {
			s, ctx, pool, lease, snapshot := cloneSnapshotRestoreFixture(t)
			if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, 2); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx, lease, snapshot.SourceDatabaseID); err != nil {
				t.Fatal(err)
			}
			lease = cloneForkCompensation(t, s, ctx, lease)
			if action != "begin" {
				if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID); err != nil {
					t.Fatal(err)
				}
			}
			proof := state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{TargetProviderResourceID: "provider/target", OperationIDs: []string{"delete-a"}, Done: true}
			if action == "operations" || action == "finish" {
				if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(ctx, lease, snapshot.SourceDatabaseID, cloneSnapshotRestoreObservation(snapshot)); err != nil {
					t.Fatal(err)
				}
			}
			if action == "finish" {
				if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(ctx, lease, snapshot.SourceDatabaseID, proof); err != nil {
					t.Fatal(err)
				}
			}
			if err := pool.QueryRow(ctx, "update project_environment_clone_operations set lease_until=clock_timestamp()+interval '400 milliseconds' where id=$1 returning lease_until", lease.Operation.ID).Scan(&lease.ExpiresAt); err != nil {
				t.Fatal(err)
			}
			lock, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lock.Rollback(context.WithoutCancel(ctx)) }()
			if _, err := lock.Exec(ctx, "select operation_id from project_environment_clone_postgres_snapshot_restores where operation_id=$1 for update", lease.Operation.ID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var err error
				switch action {
				case "begin":
					_, err = s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID)
				case "identity":
					_, err = s.RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(ctx, lease, snapshot.SourceDatabaseID, cloneSnapshotRestoreObservation(snapshot))
				case "operations":
					_, err = s.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(ctx, lease, snapshot.SourceDatabaseID, proof)
				case "finish":
					_, err = s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID, proof)
				}
				done <- err
			}()
			deadline := time.Now().Add(2 * time.Second)
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, "select exists(select 1 from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and query like '%ReadProjectEnvironmentClonePostgresSnapshotRestore%')").Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("finished before fork row lock: %v", err)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("worker did not reach fork row lock")
				}
				time.Sleep(5 * time.Millisecond)
			}
			time.Sleep(time.Until(lease.ExpiresAt) + 40*time.Millisecond)
			if err := lock.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, state.ErrConflict) {
				t.Fatalf("expired %s accepted: %v", action, err)
			}
		})
	}
}

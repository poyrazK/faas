//go:build !no_pg

// adr: 581
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgClonePostgresSnapshotCopyTargetCleanupUnknownOutcomeAndRetirement(t *testing.T) {
	s, ctx, pool, lease, snapshot := cloneCopyTargetFixture(t)
	r, _, err := s.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClaimProjectEnvironmentClonePostgresCopyTargetRequest(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("capture allowed cleanup: %v", err)
	}
	lease = cloneForkCompensation(t, s, ctx, lease)
	r, err = s.BeginProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, lease, snapshot.SourceDatabaseID)
	if err != nil || r.State != "deleting" || r.DeletionStartedAt.IsZero() || r.ProviderResourceID != "" {
		t.Fatalf("unknown creation intent: %+v %v", r, err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unknown project released input: %v", err)
	}
	proof := state.ProjectEnvironmentClonePostgresCopyTargetDeletion{ProviderResourceID: "independent-project", CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if _, err := s.FinishProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, lease, snapshot.SourceDatabaseID, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unknown outcome supplied retirement: %v", err)
	}
	r, err = s.RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentity(ctx, lease, snapshot.SourceDatabaseID, proof)
	if err != nil || r.ProviderResourceID != proof.ProviderResourceID || !r.ProviderCreatedAt.Equal(proof.CreatedAt) {
		t.Fatalf("cleanup pin: %+v %v", r, err)
	}
	databases, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	d, err := databases.Get(ctx, r.AccountID, r.TargetDatabaseID)
	if err != nil || d.ProviderResourceID != proof.ProviderResourceID || d.State != managedpostgres.StateProvisioning || d.DataResourceID != "" {
		t.Fatalf("identity was not atomic/private: %+v %v", d, err)
	}
	old := lease
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	lease, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	proof.Done = true
	if _, err := s.FinishProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, old, snapshot.SourceDatabaseID, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old worker retired owner: %v", err)
	}
	proof.Done = false
	if _, err := s.FinishProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, lease, snapshot.SourceDatabaseID, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("pending outcome released quota: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "select count(*) from managed_postgres_databases where account_id=$1 and state<>'deleted'", r.AccountID).Scan(&count); err != nil || count != 3 {
		t.Fatalf("pending quota: %d %v", count, err)
	}
	proof.Done = true
	r, err = s.FinishProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, lease, snapshot.SourceDatabaseID, proof)
	if err != nil || r.State != "retired" || r.DeletionObservedAt.IsZero() || !r.ProviderCreatedAt.Equal(proof.CreatedAt) || r.RequestStartedAt.IsZero() {
		t.Fatalf("authenticated retirement: %+v %v", r, err)
	}
	d, err = databases.Get(ctx, r.AccountID, r.TargetDatabaseID)
	if err != nil || d.State != managedpostgres.StateDeleted || d.DeletedAt == nil || d.ProviderResourceID != proof.ProviderResourceID {
		t.Fatalf("receipt/catalogue retirement: %+v %v", d, err)
	}
	if err := pool.QueryRow(ctx, "select count(*) from managed_postgres_databases where account_id=$1 and state<>'deleted'", r.AccountID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("retirement released wrong quota: %d %v", count, err)
	}
	fork, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	forkProof := state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{TargetProviderResourceID: fork.TargetProviderResourceID, OperationIDs: []string{"owned-delete"}, Done: true}
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(ctx, lease, snapshot.SourceDatabaseID, forkProof); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID, forkProof); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	if replay, err := s.FinishProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, lease, snapshot.SourceDatabaseID, proof); err != nil || replay != r {
		t.Fatalf("retirement lost proof after input cleanup: %+v %v", replay, err)
	}
}

func TestPgClonePostgresSnapshotCopyTargetCleanupRejectsSubstitutionAndKeepsPreparation(t *testing.T) {
	s, ctx, pool, lease, snapshot := cloneCopyTargetFixture(t)
	r, _, err := s.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClaimProjectEnvironmentClonePostgresCopyTargetRequest(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	o := state.ProjectEnvironmentClonePostgresCopyTargetObservation{ProviderResourceID: "independent-project", CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Prepared: true}
	r, err = s.RecordProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, o)
	if err != nil {
		t.Fatal(err)
	}
	preparedAt := r.PreparedAt
	lease = cloneForkCompensation(t, s, ctx, lease)
	r, err = s.BeginProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, lease, snapshot.SourceDatabaseID)
	if err != nil || !r.PreparedAt.Equal(preparedAt) {
		t.Fatalf("begin erased preparation: %+v %v", r, err)
	}
	proof := state.ProjectEnvironmentClonePostgresCopyTargetDeletion{ProviderResourceID: o.ProviderResourceID, CreatedAt: o.CreatedAt, Done: true}
	for _, mode := range []string{"id", "time", "capture", "future", "pending"} {
		bad := proof
		switch mode {
		case "id":
			bad.ProviderResourceID = "replacement"
		case "time":
			bad.CreatedAt = bad.CreatedAt.Add(-time.Microsecond)
		case "capture":
			var id string
			if err := pool.QueryRow(ctx, "select target_provider_resource_id from project_environment_clone_postgres_snapshot_restores where operation_id=$1", lease.Operation.ID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			bad.ProviderResourceID = id
		case "future":
			bad.CreatedAt = time.Now().Add(time.Hour).Truncate(time.Microsecond)
		case "pending":
			bad.Done = false
		}
		if _, err := s.FinishProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, lease, snapshot.SourceDatabaseID, bad); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("accepted %s: %v", mode, err)
		}
	}
	if _, err := s.RetireUndispatchedProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("remote target retired locally: %v", err)
	}
	retired, err := s.FinishProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, lease, snapshot.SourceDatabaseID, proof)
	if err != nil || !retired.PreparedAt.Equal(preparedAt) {
		t.Fatalf("retirement erased preparation: %+v %v", retired, err)
	}
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set storage_limit_bytes=123456 where id=$1", r.TargetDatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresCopyTargetForLease(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("retired replay accepted changed metadata: %v", err)
	}
}

func TestPgClonePostgresSnapshotCopyTargetCleanupChecksLeaseAfterOwnedLocks(t *testing.T) {
	for _, mode := range []string{"begin_receipt", "identity_catalogue", "finish_catalogue"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx, pool, lease, snapshot := cloneCopyTargetFixture(t)
			r, _, err := s.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, 3)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.ClaimProjectEnvironmentClonePostgresCopyTargetRequest(ctx, lease, snapshot.SourceDatabaseID); err != nil {
				t.Fatal(err)
			}
			lease = cloneForkCompensation(t, s, ctx, lease)
			proof := state.ProjectEnvironmentClonePostgresCopyTargetDeletion{ProviderResourceID: "independent-project", CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Done: true}
			if mode != "begin_receipt" {
				if _, err := s.BeginProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, lease, snapshot.SourceDatabaseID); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "finish_catalogue" {
				if _, err := s.RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentity(ctx, lease, snapshot.SourceDatabaseID, proof); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.ProjectEnvironmentClonePostgresCopyTargetForLease(ctx, lease, snapshot.SourceDatabaseID)
			if err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, "update project_environment_clone_operations set lease_until=clock_timestamp()+interval '400 milliseconds' where id=$1 returning lease_until", lease.Operation.ID).Scan(&lease.ExpiresAt); err != nil {
				t.Fatal(err)
			}
			lock, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lock.Rollback(context.WithoutCancel(ctx)) }()
			needle := "LockManagedPostgresLifecycleDatabase"
			if mode == "begin_receipt" {
				needle = "ReadProjectEnvironmentClonePostgresCopyTarget"
				_, err = lock.Exec(ctx, "select operation_id from project_environment_clone_postgres_copy_targets where operation_id=$1 for update", lease.Operation.ID)
			} else {
				_, err = lock.Exec(ctx, "select id from managed_postgres_databases where id=$1 for update", r.TargetDatabaseID)
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var err error
				switch mode {
				case "begin_receipt":
					_, err = s.BeginProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, lease, snapshot.SourceDatabaseID)
				case "identity_catalogue":
					_, err = s.RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentity(ctx, lease, snapshot.SourceDatabaseID, proof)
				case "finish_catalogue":
					_, err = s.FinishProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, lease, snapshot.SourceDatabaseID, proof)
				}
				done <- err
			}()
			deadline := time.Now().Add(2 * time.Second)
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, "select exists(select 1 from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and query like $1)", "%"+needle+"%").Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("finished before owned lock: %v", err)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("did not reach owned lock")
				}
				time.Sleep(5 * time.Millisecond)
			}
			time.Sleep(time.Until(lease.ExpiresAt) + 40*time.Millisecond)
			if err := lock.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, state.ErrConflict) {
				t.Fatalf("expired %s mutated: %v", mode, err)
			}
			lease, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if after, err := s.ProjectEnvironmentClonePostgresCopyTargetForLease(ctx, lease, snapshot.SourceDatabaseID); err != nil || after != before {
				t.Fatalf("expired transaction changed owner: %+v %v", after, err)
			}
		})
	}
}

//go:build !no_pg

// adr:567
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func cloneSnapshotRestoreFixture(t *testing.T, major ...int) (*state.PgStore, context.Context, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresSnapshot) {
	t.Helper()
	s, ctx, pool, lease, sourceID := clonePostgresSnapshotFixture(t, major...)
	r, err := s.ReserveProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClaimProjectEnvironmentClonePostgresSnapshotRequest(ctx, lease, sourceID); err != nil {
		t.Fatal(err)
	}
	r, err = s.RecordProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID, clonePostgresSnapshotObservation(r))
	if err != nil {
		t.Fatal(err)
	}
	return s, ctx, pool, lease, r
}

func cloneSnapshotRestoreObservation(r state.ProjectEnvironmentClonePostgresSnapshot) state.ProjectEnvironmentClonePostgresSnapshotRestoreObservation {
	return state.ProjectEnvironmentClonePostgresSnapshotRestoreObservation{ProviderSnapshotID: r.ProviderSnapshotID, SourceDataResourceID: r.SourceDataResourceID,
		TargetProviderResourceID: "provider/target", CapturePoint: r.CapturePoint, SnapshotCreatedAt: r.SnapshotCreatedAt, TargetCreatedAt: r.SnapshotCreatedAt}
}

func TestPgClonePostgresSnapshotRestoreRecoveryQuotaAndCleanupHold(t *testing.T) {
	s, ctx, pool, lease, snapshot := cloneSnapshotRestoreFixture(t)
	if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, 1); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("fork bypassed source quota: %v", err)
	}
	// Immutable snapshots continue to work after finite source PITR expires.
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set restore_window_seconds=0 where id=$1", snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, 2)
	if err != nil || receipt.State != "reserved" || receipt.TargetOwnerID == snapshot.SourceDatabaseID || receipt.RequestStartedAt != (time.Time{}) {
		t.Fatalf("reserve: %+v %v", receipt, err)
	}
	if replay, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, 1); err != nil || replay != receipt {
		t.Fatalf("replay rebased intent: %+v %v", replay, err)
	}
	store, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	input := managedpostgres.Database{ID: uuid.NewString(), AccountID: lease.Operation.AccountID, Name: "other-database", State: managedpostgres.StateProvisioning,
		BackendID: receipt.BackendID, BackendFingerprint: strings.Repeat("a", 64), DesiredGeneration: 1,
		Spec: managedpostgres.Spec{Region: "eu-central-1", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if _, _, err := store.Reserve(ctx, input, 2); !errors.Is(err, managedpostgres.ErrQuotaExceeded) {
		t.Fatalf("ordinary database ignored private fork quota: %v", err)
	}
	receipt, dispatch, err := s.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx, lease, snapshot.SourceDatabaseID)
	if err != nil || !dispatch || receipt.State != "requested" || receipt.RequestStartedAt.IsZero() {
		t.Fatalf("dispatch: %+v %v %v", receipt, dispatch, err)
	}
	if replay, dispatch, err := s.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx, lease, snapshot.SourceDatabaseID); err != nil || dispatch || replay != receipt {
		t.Fatalf("redispatched: %+v %v %v", replay, dispatch, err)
	}
	observed := cloneSnapshotRestoreObservation(snapshot)
	receipt, err = s.RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, observed)
	if err != nil || receipt.State != "restoring" || receipt.ObservedAt.IsZero() || !receipt.RestoredAt.IsZero() {
		t.Fatalf("pending: %+v %v", receipt, err)
	}
	old := lease
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	lease, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, old, snapshot.SourceDatabaseID, observed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker recorded fork: %v", err)
	}
	if actual, err := s.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, snapshot.SourceDatabaseID); err != nil || actual != receipt {
		t.Fatalf("worker handoff: %+v %v", actual, err)
	}
	observed.Restored = true
	receipt, err = s.RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, observed)
	if err != nil || receipt.State != "restored" || receipt.RestoredAt.IsZero() {
		t.Fatalf("restored: %+v %v", receipt, err)
	}
	first := receipt.RestoredAt
	if replay, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, observed); err != nil || !replay.RestoredAt.Equal(first) {
		t.Fatalf("replay moved completion time: %+v %v", replay, err)
	}
	op := lease.Operation
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, op.Resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unadopted native fork advanced to copying: %v", err)
	}
	lease.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("discarded snapshot with unresolved fork: %v", err)
	}
	if _, _, err := s.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("compensation dispatched new fork: %v", err)
	}
	if actual, err := s.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, snapshot.SourceDatabaseID); err != nil || actual.TargetOwnerID != receipt.TargetOwnerID {
		t.Fatalf("compensation lost recovery identity: %+v %v", actual, err)
	}
	op = lease.Operation
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensated, op.Revision, nil, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unretired fork discarded worker recovery authority: %v", err)
	}
}

func TestPgClonePostgresSnapshotRestoreRejectsSubstitutedProof(t *testing.T) {
	s, ctx, _, lease, snapshot := cloneSnapshotRestoreFixture(t)
	if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, 2); err != nil {
		t.Fatal(err)
	}
	observed := cloneSnapshotRestoreObservation(snapshot)
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, observed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unrequested fork recorded: %v", err)
	}
	if _, _, err := s.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"snapshot", "source", "source_target", "lifecycle_target", "target_missing", "point", "snapshot_time", "target_time", "future", "precision"} {
		t.Run(fault, func(t *testing.T) {
			o := observed
			switch fault {
			case "snapshot":
				o.ProviderSnapshotID += "other"
			case "source":
				o.SourceDataResourceID += "other"
			case "source_target":
				o.TargetProviderResourceID = snapshot.SourceDataResourceID
			case "lifecycle_target":
				o.TargetProviderResourceID = snapshot.SourceProviderResourceID
			case "target_missing":
				o.TargetProviderResourceID = ""
			case "point":
				o.CapturePoint = o.CapturePoint.Add(time.Microsecond)
			case "snapshot_time":
				o.SnapshotCreatedAt = o.SnapshotCreatedAt.Add(time.Microsecond)
			case "target_time":
				o.TargetCreatedAt = o.SnapshotCreatedAt.Add(-time.Microsecond)
			case "future":
				o.TargetCreatedAt = time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
			case "precision":
				o.TargetCreatedAt = o.TargetCreatedAt.Add(time.Nanosecond)
			}
			if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, o); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("accepted %s: %v", fault, err)
			}
		})
	}
	observed.Restored = true
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, observed); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"target", "time", "regression"} {
		o := observed
		switch fault {
		case "target":
			o.TargetProviderResourceID += "other"
		case "time":
			o.TargetCreatedAt = o.TargetCreatedAt.Add(time.Microsecond)
		case "regression":
			o.Restored = false
		}
		if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, o); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("accepted %s after completion: %v", fault, err)
		}
	}
}

func TestPgClonePostgresSnapshotRestoreRequiresRetainedCaptureAndFrozenPlacement(t *testing.T) {
	s, ctx, pool, lease, sourceID := clonePostgresSnapshotFixture(t)
	if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, sourceID, 2); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("fork without snapshot: %v", err)
	}
	snapshot, err := s.ReserveProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClaimProjectEnvironmentClonePostgresSnapshotRequest(ctx, lease, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, sourceID, 2); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("fork before retained snapshot: %v", err)
	}
	snapshot, err = s.RecordProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID, clonePostgresSnapshotObservation(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set data_resource_id='provider/other' where id=$1", sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, sourceID, 2); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed placement adopted: %v", err)
	}
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set data_resource_id=$2 where id=$1", sourceID, snapshot.SourceDataResourceID); err != nil {
		t.Fatal(err)
	}
	original, err := json.Marshal(lease.Operation.Resources)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update project_environment_clone_operations set resources=jsonb_set(resources,'{0,target_id}',to_jsonb($2::text)) where id=$1", lease.Operation.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, sourceID, 2); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("capture with catalogue target adopted: %v", err)
	}
	if _, err := pool.Exec(ctx, "update project_environment_clone_operations set resources=$2::jsonb where id=$1", lease.Operation.ID, original); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, sourceID, 2); err != nil {
		t.Fatalf("valid retained capture rejected: %v", err)
	}
}

func TestPgClonePostgresSnapshotRestoreChecksLeaseAfterLockWait(t *testing.T) {
	for _, action := range []string{"reserve", "read", "claim", "record"} {
		t.Run(action, func(t *testing.T) {
			s, ctx, pool, lease, snapshot := cloneSnapshotRestoreFixture(t)
			if action != "reserve" {
				if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, 2); err != nil {
					t.Fatal(err)
				}
			}
			if action == "record" {
				if _, _, err := s.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx, lease, snapshot.SourceDatabaseID); err != nil {
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
			needle := "ReadProjectEnvironmentClonePostgresSnapshotRestore"
			if action == "reserve" {
				needle = "ReadProjectEnvironmentCloneDatabaseSource"
				_, err = lock.Exec(ctx, "select id from managed_postgres_databases where id=$1 for update", snapshot.SourceDatabaseID)
			} else {
				_, err = lock.Exec(ctx, "select operation_id from project_environment_clone_postgres_snapshot_restores where operation_id=$1 for update", lease.Operation.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var err error
				switch action {
				case "reserve":
					_, err = s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, 2)
				case "read":
					_, err = s.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, snapshot.SourceDatabaseID)
				case "claim":
					_, _, err = s.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx, lease, snapshot.SourceDatabaseID)
				case "record":
					_, err = s.RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, cloneSnapshotRestoreObservation(snapshot))
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
					t.Fatalf("finished before row lock: %v", err)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("worker did not reach owned row lock")
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
			var count int
			if err := pool.QueryRow(ctx, "select count(*) from project_environment_clone_postgres_snapshot_restores where operation_id=$1 and (request_started_at is not null or observed_at is not null)", lease.Operation.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if action == "record" {
				want = 1
			}
			if count != want {
				t.Fatalf("expired worker changed receipt: count=%d want=%d", count, want)
			}
		})
	}
}

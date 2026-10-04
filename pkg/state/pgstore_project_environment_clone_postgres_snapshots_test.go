//go:build !no_pg

// adr: 567
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func clonePostgresSnapshotFixture(t *testing.T, major ...int) (*state.PgStore, context.Context, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, string) {
	t.Helper()
	s, ctx, pool := pgWithPool(t)
	a, p, app, op := cloneBindingFixture(t, s)
	secret := clonePostgresSecretFixture(t, pool, a, app, "production", "snapshot-source", 1)
	if err := s.PutManagedPostgresSecret(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if len(major) != 0 {
		if _, err := pool.Exec(ctx, "update managed_postgres_databases set postgres_major=$1 where id=(select database_id from managed_postgres_bindings where id=$2)", major[0], secret.ManagedPostgresBindingID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision); err != nil {
		t.Fatal(err)
	}
	bindings, err := s.ProjectEnvironmentCloneBindings(ctx, a.ID, p.ID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	source := bindings[0].Postgres[0]
	hash, err := state.ProjectEnvironmentCloneDatabaseSourceHash(source)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	point := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)
	resources := []state.ProjectEnvironmentCloneResource{{Kind: "managed_postgres", Name: source.DatabaseID, SourceID: source.DatabaseID,
		SourceVersion: hash, CapturePoint: point.Format(time.RFC3339Nano), Status: "captured"}}
	lease.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, op.Status, lease.Operation.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	return s, ctx, pool, lease, source.DatabaseID
}

func clonePostgresSnapshotObservation(receipt state.ProjectEnvironmentClonePostgresSnapshot) state.ProjectEnvironmentClonePostgresSnapshotObservation {
	return state.ProjectEnvironmentClonePostgresSnapshotObservation{ProviderSnapshotID: "provider-snapshot/" + receipt.OperationID,
		SourceDataResourceID: receipt.SourceDataResourceID, CapturePoint: receipt.CapturePoint, CreatedAt: receipt.CapturePoint.Add(time.Second)}
}

func TestPgClonePostgresSnapshotRecoveryAndSourceHold(t *testing.T) {
	s, ctx, pool, lease, sourceID := clonePostgresSnapshotFixture(t)
	receipt, err := s.ReserveProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID)
	if err != nil || receipt.State != "capturing" || receipt.ProviderSnapshotID != "" {
		t.Fatalf("reserve = %+v, %v", receipt, err)
	}
	if replay, err := s.ReserveProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID); err != nil || replay != receipt {
		t.Fatalf("intent replay = %+v, %v", replay, err)
	}
	databases, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	// Remove ordinary binding dependencies so only the snapshot intent holds
	// the lifecycle. This covers an unknown provider creation outcome too.
	if _, err := pool.Exec(ctx, "update managed_postgres_bindings set state='deleted',deleted_at=clock_timestamp() where database_id=$1", sourceID); err != nil {
		t.Fatal(err)
	}
	assertHeld := func() {
		t.Helper()
		now := time.Now().UTC()
		if _, err := databases.ClaimDelete(ctx, lease.Operation.AccountID, sourceID, uuid.NewString(), now, now.Add(time.Minute)); !errors.Is(err, managedpostgres.ErrConflict) {
			t.Fatalf("snapshot failed to hold source: %v", err)
		}
	}
	assertHeld()
	receipt, dispatch, err := s.ClaimProjectEnvironmentClonePostgresSnapshotRequest(ctx, lease, sourceID)
	if err != nil || !dispatch {
		t.Fatalf("dispatch = %v, %v", dispatch, err)
	}
	observed := clonePostgresSnapshotObservation(receipt)
	retained, err := s.RecordProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID, observed)
	if err != nil || retained.State != "retained" || retained.ProviderSnapshotID != observed.ProviderSnapshotID || retained.ObservedAt.IsZero() {
		t.Fatalf("retained = %+v, %v", retained, err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID, observed); err != nil {
		t.Fatal(err)
	}
	assertHeld()
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, sourceID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("capture discarded snapshot: %v", err)
	}
	old := lease
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	lease, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshot(ctx, old, sourceID, observed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old worker wrote receipt: %v", err)
	}
	read, err := s.ProjectEnvironmentClonePostgresSnapshotForLease(ctx, lease, sourceID)
	if err != nil || read.ProviderSnapshotID != retained.ProviderSnapshotID || !read.CapturePoint.Equal(retained.CapturePoint) {
		t.Fatalf("recovery = %+v, %v", read, err)
	}
	// Replay uses the retained receipt even when live PITR history expires.
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set restore_window_seconds=0 where id=$1", sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID); err != nil {
		t.Fatalf("retained replay reread finite history: %v", err)
	}
	op := lease.Operation
	lease.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, sourceID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unbegun deletion completed: %v", err)
	}
	deleting, err := s.BeginProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, sourceID)
	if err != nil || deleting.State != "deleting" {
		t.Fatalf("cleanup intent = %+v, %v", deleting, err)
	}
	assertHeld()
	deleted, err := s.FinishProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, sourceID)
	if err != nil || deleted.State != "deleted" || deleted.CleanupObservedAt.IsZero() {
		t.Fatalf("cleanup = %+v, %v", deleted, err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, sourceID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := databases.ClaimDelete(ctx, op.AccountID, sourceID, uuid.NewString(), now, now.Add(time.Minute)); err != nil {
		t.Fatalf("observed cleanup kept source held: %v", err)
	}
}

func TestPgClonePostgresSnapshotRejectsChangedEvidenceAndAuthority(t *testing.T) {
	s, ctx, _, lease, sourceID := clonePostgresSnapshotFixture(t)
	receipt, err := s.ReserveProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _, err = s.ClaimProjectEnvironmentClonePostgresSnapshotRequest(ctx, lease, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"id", "source", "point", "created", "precision", "expiry"} {
		t.Run(fault, func(t *testing.T) {
			observed := clonePostgresSnapshotObservation(receipt)
			switch fault {
			case "id":
				observed.ProviderSnapshotID = ""
			case "source":
				observed.SourceDataResourceID += "-other"
			case "point":
				observed.CapturePoint = observed.CapturePoint.Add(time.Microsecond)
			case "created":
				observed.CreatedAt = receipt.CapturePoint.Add(-time.Microsecond)
			case "precision":
				observed.CreatedAt = observed.CreatedAt.Add(time.Nanosecond)
			case "expiry":
				expiry := time.Now().Add(time.Hour)
				observed.ExpiresAt = &expiry
			}
			if _, err := s.RecordProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID, observed); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("accepted %s: %v", fault, err)
			}
		})
	}
	for _, fault := range []string{"token", "revision", "status", "account", "project"} {
		bad := lease
		switch fault {
		case "token":
			bad.Token = uuid.NewString()
		case "revision":
			bad.Operation.Revision++
		case "status":
			bad.Operation.Status = state.CloneOperationCopying
		case "account":
			bad.Operation.AccountID = uuid.NewString()
		case "project":
			bad.Operation.ProjectID = uuid.NewString()
		}
		if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshot(ctx, bad, sourceID); err == nil {
			t.Fatalf("%s reserved", fault)
		}
		if _, err := s.ProjectEnvironmentClonePostgresSnapshotForLease(ctx, bad, sourceID); err == nil {
			t.Fatalf("%s read", fault)
		}
		if _, err := s.RecordProjectEnvironmentClonePostgresSnapshot(ctx, bad, sourceID, clonePostgresSnapshotObservation(receipt)); err == nil {
			t.Fatalf("%s retained", fault)
		}
	}
	observed := clonePostgresSnapshotObservation(receipt)
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID, observed); err != nil {
		t.Fatal(err)
	}
	observed.ProviderSnapshotID += "-changed"
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID, observed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("identity replaced: %v", err)
	}
}

func TestPgClonePostgresSnapshotReservationRejectsSourceChanges(t *testing.T) {
	for _, mutation := range []string{"state='deleting'", "data_resource_id='changed'", "provider_resource_id='changed'", "backend_fingerprint=repeat('f',64)", "restore_window_seconds=0"} {
		t.Run(mutation, func(t *testing.T) {
			s, ctx, pool, lease, sourceID := clonePostgresSnapshotFixture(t)
			if _, err := pool.Exec(ctx, "update managed_postgres_databases set "+mutation+" where id=$1", sourceID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("changed source reserved: %v", err)
			}
			var count int
			if err := pool.QueryRow(ctx, "select count(*) from project_environment_clone_postgres_snapshots where operation_id=$1", lease.Operation.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rejection committed intent: count=%d err=%v", count, err)
			}
		})
	}
}

func TestPgClonePostgresSnapshotRejectsExpiredLeaseAfterSourceLock(t *testing.T) {
	s, ctx, pool, lease, sourceID := clonePostgresSnapshotFixture(t)
	// Renewal extends an existing minute lease; it cannot shorten it. Set an
	// explicit test deadline before confirming the blocked worker's lock wait.
	if err := pool.QueryRow(ctx, "update project_environment_clone_operations set lease_until=clock_timestamp()+interval '400 milliseconds' where id=$1 returning lease_until", lease.Operation.ID).Scan(&lease.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := lock.Exec(ctx, "select id from managed_postgres_databases where id=$1 for update", sourceID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.ReserveProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID); done <- err }()
	waitClonePostgresSnapshotLock(t, ctx, pool)
	time.Sleep(time.Until(lease.ExpiresAt) + 50*time.Millisecond)
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatalf("post-lock expiry accepted: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "select count(*) from project_environment_clone_postgres_snapshots where operation_id=$1", lease.Operation.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired reservation committed: count=%d err=%v", count, err)
	}
}

func TestPgClonePostgresSnapshotMutationsExpireWhileWaitingOnReceipt(t *testing.T) {
	for _, phase := range []string{"dispatch", "retain", "begin_cleanup", "finish_cleanup"} {
		t.Run(phase, func(t *testing.T) {
			s, ctx, pool, lease, sourceID := clonePostgresSnapshotFixture(t)
			receipt, err := s.ReserveProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID)
			if err != nil {
				t.Fatal(err)
			}
			if phase != "dispatch" {
				receipt, _, err = s.ClaimProjectEnvironmentClonePostgresSnapshotRequest(ctx, lease, sourceID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == "begin_cleanup" || phase == "finish_cleanup" {
				op := lease.Operation
				lease.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, "")
				if err != nil {
					t.Fatal(err)
				}
				if phase == "finish_cleanup" {
					receipt, err = s.BeginProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, sourceID)
					if err != nil {
						t.Fatal(err)
					}
					receipt, err = s.RecordProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID, clonePostgresSnapshotObservation(receipt))
					if err != nil {
						t.Fatal(err)
					}
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
			if _, err := lock.Exec(ctx, "select operation_id from project_environment_clone_postgres_snapshots where operation_id=$1 for update", lease.Operation.ID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var err error
				switch phase {
				case "dispatch":
					_, _, err = s.ClaimProjectEnvironmentClonePostgresSnapshotRequest(ctx, lease, sourceID)
				case "retain":
					_, err = s.RecordProjectEnvironmentClonePostgresSnapshot(ctx, lease, sourceID, clonePostgresSnapshotObservation(receipt))
				case "begin_cleanup":
					_, err = s.BeginProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, sourceID)
				case "finish_cleanup":
					_, err = s.FinishProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, sourceID)
				}
				done <- err
			}()
			waitClonePostgresSnapshotLock(t, ctx, pool)
			time.Sleep(time.Until(lease.ExpiresAt) + 50*time.Millisecond)
			if err := lock.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, state.ErrConflict) {
				t.Fatalf("expired %s accepted: %v", phase, err)
			}
			var actualState string
			if err := pool.QueryRow(ctx, "select state from project_environment_clone_postgres_snapshots where operation_id=$1", lease.Operation.ID).Scan(&actualState); err != nil || actualState != receipt.State {
				t.Fatalf("expired mutation changed state: state=%s err=%v", actualState, err)
			}
		})
	}
}

func waitClonePostgresSnapshotLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := pool.QueryRow(ctx, `select exists(select 1 from pg_stat_activity where datname=current_database()
            and pid<>pg_backend_pid() and wait_event_type='Lock' and (query like '%ReadProjectEnvironmentCloneDatabaseSource%'
                or query like '%ReadProjectEnvironmentClonePostgresSnapshot%'))`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("snapshot writer never waited on the held receipt/source lock")
}

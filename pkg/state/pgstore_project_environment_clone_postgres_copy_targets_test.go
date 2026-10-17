//go:build !no_pg

// adr: 590
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

func cloneCopyTargetFixture(t *testing.T) (*state.PgStore, context.Context, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresSnapshot) {
	t.Helper()
	s, ctx, pool, lease, snapshot := cloneNativeAdoptionFixture(t)
	if _, _, err := s.AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	return s, ctx, pool, lease, snapshot
}

func TestPgClonePostgresSnapshotCopyTargetFrozenQuotaDispatchAndHandoff(t *testing.T) {
	s, ctx, pool, lease, snapshot := cloneCopyTargetFixture(t)
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set restore_window_seconds=0,storage_limit_bytes=123456 where id=$1", snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, 2); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("independent target ignored capture quota: %v", err)
	}
	r, created, err := s.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, 3)
	if err != nil || !created || r.State != "reserved" || r.TargetDatabaseID == r.CaptureDatabaseID || r.TargetDatabaseID == snapshot.SourceDatabaseID {
		t.Fatalf("independent reservation: %+v %v %v", r, created, err)
	}
	if replay, created, err := s.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, 1); err != nil || created || replay != r {
		t.Fatalf("reply loss replaced target: %+v %v %v", replay, created, err)
	}
	databases, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	d, err := databases.Get(ctx, r.AccountID, r.TargetDatabaseID)
	if err != nil || d.Spec.StorageLimitBytes == 123456 || d.Spec.RestoreWindowSeconds == 0 || d.DataResourceID != "" || d.ProviderResourceID != "" || d.ObservedGeneration != 0 {
		t.Fatalf("independent target reselected live spec: %+v %v", d, err)
	}
	if _, err := databases.GetCustomerDatabase(ctx, r.AccountID, r.TargetDatabaseID); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("private target became visible: %v", err)
	}
	now := time.Now().UTC()
	if _, err := databases.Claim(ctx, r.AccountID, r.TargetDatabaseID, uuid.NewString(), managedpostgres.StateProvisioning, now, now.Add(time.Minute)); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("generic PITR provisioner claimed target: %v", err)
	}
	if _, err := databases.ClaimDelete(ctx, r.AccountID, r.TargetDatabaseID, uuid.NewString(), now, now.Add(time.Minute)); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("generic deletion claimed target: %v", err)
	}
	due, err := databases.Due(ctx, true, 100, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range due {
		if d.ID == r.TargetDatabaseID {
			t.Fatal("generic lifecycle discovered preparation owner")
		}
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, state.ProjectEnvironmentClonePostgresCopyTargetObservation{ProviderResourceID: "independent-project", CreatedAt: snapshot.SnapshotCreatedAt, Prepared: true}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("undispatched proof accepted: %v", err)
	}
	r, dispatch, err := s.ClaimProjectEnvironmentClonePostgresCopyTargetRequest(ctx, lease, snapshot.SourceDatabaseID)
	if err != nil || !dispatch || r.State != "requested" || r.RequestStartedAt.IsZero() {
		t.Fatalf("first dispatch: %+v %v %v", r, dispatch, err)
	}
	if replay, dispatch, err := s.ClaimProjectEnvironmentClonePostgresCopyTargetRequest(ctx, lease, snapshot.SourceDatabaseID); err != nil || dispatch || replay != r {
		t.Fatalf("repeated create intent: %+v %v %v", replay, dispatch, err)
	}
	old := lease
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	lease, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClaimProjectEnvironmentClonePostgresCopyTargetRequest(ctx, old, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker retained dispatch: %v", err)
	}
	if replay, dispatch, err := s.ClaimProjectEnvironmentClonePostgresCopyTargetRequest(ctx, lease, snapshot.SourceDatabaseID); err != nil || dispatch || replay != r {
		t.Fatalf("handoff repeated dispatch: %+v %v %v", replay, dispatch, err)
	}
	o := state.ProjectEnvironmentClonePostgresCopyTargetObservation{ProviderResourceID: "independent-project", CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	r, err = s.RecordProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, o)
	if err != nil || r.State != "preparing" || r.ProviderResourceID != o.ProviderResourceID {
		t.Fatalf("pending observation: %+v %v", r, err)
	}
	o.Prepared = true
	r, err = s.RecordProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, o)
	if err != nil || r.State != "prepared" || r.PreparedAt.IsZero() {
		t.Fatalf("prepared project: %+v %v", r, err)
	}
	firstPrepared := r.PreparedAt
	if r, err = s.RecordProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, o); err != nil || !r.PreparedAt.Equal(firstPrepared) {
		t.Fatalf("preparation proof moved on replay: %+v %v", r, err)
	}
	d, err = databases.Get(ctx, r.AccountID, r.TargetDatabaseID)
	if err != nil || d.State != managedpostgres.StateProvisioning || d.ObservedGeneration != 0 || d.DataResourceID != "" || d.ProviderResourceID != o.ProviderResourceID {
		t.Fatalf("empty project forged data readiness: %+v %v", d, err)
	}
	if _, err := databases.GetCloneRestoreProof(ctx, r.AccountID, r.TargetDatabaseID); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("project preparation forged PITR proof: %v", err)
	}
	op := lease.Operation
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, op.Resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("empty target advanced capture: %v", err)
	}
	lease = cloneForkCompensation(t, s, ctx, lease)
	if _, err := s.RetireUndispatchedProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("dispatched project discarded without remote proof: %v", err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("active target discarded immutable input: %v", err)
	}
}

func TestPgClonePostgresSnapshotCopyTargetUndispatchedRetirementKeepsOwnership(t *testing.T) {
	s, ctx, pool, lease, snapshot := cloneCopyTargetFixture(t)
	r, _, err := s.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, 3)
	if err != nil {
		t.Fatal(err)
	}
	lease = cloneForkCompensation(t, s, ctx, lease)
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("reserved target lost input: %v", err)
	}
	r, err = s.RetireUndispatchedProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID)
	if err != nil || r.State != "retired" || r.RetiredAt.IsZero() {
		t.Fatalf("local retirement: %+v %v", r, err)
	}
	if replay, err := s.RetireUndispatchedProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID); err != nil || replay != r {
		t.Fatalf("retirement replay: %+v %v", replay, err)
	}
	databases, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	d, err := databases.Get(ctx, r.AccountID, r.TargetDatabaseID)
	if err != nil || d.State != managedpostgres.StateDeleted || d.DeletedAt == nil {
		t.Fatalf("catalogue did not retire atomically: %+v %v", d, err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatalf("retired target retained capture hold: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "select count(*) from managed_postgres_databases where account_id=$1 and state<>'deleted'", r.AccountID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("local retirement released wrong quota: %d %v", count, err)
	}
	if replay, err := s.ProjectEnvironmentClonePostgresCopyTargetForLease(ctx, lease, snapshot.SourceDatabaseID); err != nil || replay != r {
		t.Fatalf("retired target lost receipt during native cleanup: %+v %v", replay, err)
	}
}

func TestPgClonePostgresSnapshotCopyTargetRejectsForeignAndChangedMetadata(t *testing.T) {
	for _, mode := range []string{"foreign_name", "provider", "storage", "data", "generation", "source", "capture", "observed_pin", "time_pin", "future_time", "regression"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx, pool, lease, snapshot := cloneCopyTargetFixture(t)
			if mode == "foreign_name" {
				_, err := pool.Exec(ctx, `insert into managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint)
                    select $1,account_id,$2,region,postgres_major,service_class,availability,backend_id,backend_fingerprint from managed_postgres_databases where id=$3`, uuid.NewString(), state.ProjectEnvironmentCloneDatabaseName(lease.Operation, snapshot.SourceDatabaseID), snapshot.SourceDatabaseID)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := s.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, 4); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("adopted foreign target: %v", err)
				}
				return
			}
			r, _, err := s.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, 3)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.ClaimProjectEnvironmentClonePostgresCopyTargetRequest(ctx, lease, snapshot.SourceDatabaseID); err != nil {
				t.Fatal(err)
			}
			o := state.ProjectEnvironmentClonePostgresCopyTargetObservation{ProviderResourceID: "independent-project", CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Prepared: true}
			if _, err := s.RecordProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, o); err != nil {
				t.Fatal(err)
			}
			changes := map[string]string{"provider": "provider_resource_id='foreign-project'", "storage": "storage_limit_bytes=123456", "data": "data_resource_id='unproven/data'", "generation": "desired_generation=2", "source": "restore_source_resource_id='provider/other'"}
			if statement, ok := changes[mode]; ok {
				if _, err := pool.Exec(ctx, "update managed_postgres_databases set "+statement+" where id=$1", r.TargetDatabaseID); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ProjectEnvironmentClonePostgresCopyTargetForLease(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("accepted changed %s: %v", mode, err)
				}
				return
			}
			switch mode {
			case "capture":
				if _, err := pool.Exec(ctx, "update project_environment_clone_postgres_copy_targets set capture_database_id=$2 where operation_id=$1", lease.Operation.ID, snapshot.SourceDatabaseID); err != nil {
					t.Fatal(err)
				}
			case "observed_pin":
				o.ProviderResourceID = "replacement-project"
			case "time_pin":
				o.CreatedAt = o.CreatedAt.Add(-time.Microsecond)
			case "future_time":
				o.CreatedAt = time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
			case "regression":
				o.Prepared = false
			}
			if _, err := s.RecordProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, o); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("accepted changed %s: %v", mode, err)
			}
		})
	}
}

func TestPgClonePostgresSnapshotCopyTargetLeaseExpiresBehindOwnedLocks(t *testing.T) {
	for _, kind := range []string{"account", "receipt", "catalogue"} {
		t.Run(kind, func(t *testing.T) {
			s, ctx, pool, lease, snapshot := cloneCopyTargetFixture(t)
			var r state.ProjectEnvironmentClonePostgresCopyTarget
			if kind != "account" {
				var err error
				r, _, err = s.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, 3)
				if err != nil {
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
			var blockerPID int32
			if err := lock.QueryRow(ctx, "select pg_backend_pid()").Scan(&blockerPID); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "account":
				_, err = lock.Exec(ctx, "select id from accounts where id=$1 for update", lease.Operation.AccountID)
			case "receipt":
				_, err = lock.Exec(ctx, "select operation_id from project_environment_clone_postgres_copy_targets where operation_id=$1 for update", lease.Operation.ID)
			case "catalogue":
				_, err = lock.Exec(ctx, "select id from managed_postgres_databases where id=$1 for update", r.TargetDatabaseID)
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if kind == "account" {
					_, _, err = s.ReserveProjectEnvironmentClonePostgresCopyTarget(ctx, lease, snapshot.SourceDatabaseID, 3)
				} else {
					_, _, err = s.ClaimProjectEnvironmentClonePostgresCopyTargetRequest(ctx, lease, snapshot.SourceDatabaseID)
				}
				done <- err
			}()
			deadline := time.Now().Add(2 * time.Second)
			for {
				var waiting bool
				// Follow the actual owned blocker, including the earlier
				// account write lock, rather than a SQL comment's spelling.
				if err := pool.QueryRow(ctx, "select exists(select 1 from pg_stat_activity where datname=current_database() and $1::int=any(pg_blocking_pids(pid)))", blockerPID).Scan(&waiting); err != nil {
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
				t.Fatalf("expired mutation behind %s lock: %v", kind, err)
			}
			var count int
			if err := pool.QueryRow(ctx, "select count(*) from project_environment_clone_postgres_copy_targets where operation_id=$1 and request_started_at is not null", lease.Operation.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("expired worker dispatched: %d %v", count, err)
			}
		})
	}
}

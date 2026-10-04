//go:build !no_pg

// adr:531
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

func cloneNativeAdoptionFixture(t *testing.T, major ...int) (*state.PgStore, context.Context, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresSnapshot) {
	t.Helper()
	s, ctx, pool, lease, snapshot := cloneSnapshotRestoreFixture(t, major...)
	if _, err := s.ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	observed := cloneSnapshotRestoreObservation(snapshot)
	observed.Restored = true
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID, observed); err != nil {
		t.Fatal(err)
	}
	return s, ctx, pool, lease, snapshot
}

func TestPgClonePostgresSnapshotRestoreAdoptionTransfersQuotaAndRetiresOwnedCatalogue(t *testing.T) {
	s, ctx, pool, lease, snapshot := cloneNativeAdoptionFixture(t)
	original, err := s.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, snapshot.SourceDatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	// Adoption consumes the retained copy and frozen definition. Live desired
	// changes and expiration of source PITR cannot select a different fork.
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set restore_window_seconds=0,storage_limit_bytes=123456 where id=$1", snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	target, created, err := s.AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID)
	if err != nil || !created || target.ID != original.TargetOwnerID || target.State != "provisioning" {
		t.Fatalf("adoption: %+v %v %v", target, created, err)
	}
	receipt, err := s.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, snapshot.SourceDatabaseID)
	if err != nil || receipt.State != "adopted" || receipt.AdoptedDatabaseID != target.ID || receipt.AdoptedAt.IsZero() {
		t.Fatalf("owned catalogue receipt: %+v %v", receipt, err)
	}
	databases, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	database, err := databases.Get(ctx, lease.Operation.AccountID, target.ID)
	if err != nil || database.ProviderResourceID != receipt.TargetProviderResourceID || database.DataResourceID != "" || database.ObservedGeneration != 0 || database.Spec.StorageLimitBytes == 123456 || database.RestoreSourceResourceID != snapshot.SourceDataResourceID || !database.RestorePointInTime.Equal(snapshot.CapturePoint) {
		t.Fatalf("private frozen database: %+v %v", database, err)
	}
	if _, err := databases.GetCloneRestoreProof(ctx, database.AccountID, target.ID); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("adoption forged readiness: %v", err)
	}
	now := time.Now().UTC()
	if _, err := databases.Claim(ctx, database.AccountID, target.ID, uuid.NewString(), managedpostgres.StateProvisioning, now, now.Add(time.Minute)); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("ordinary provisioner claimed native owner: %v", err)
	}
	if _, err := databases.ClaimDelete(ctx, database.AccountID, target.ID, uuid.NewString(), now, now.Add(time.Minute)); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("ordinary cleanup claimed native owner: %v", err)
	}
	due, err := databases.Due(ctx, true, 100, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range due {
		if row.ID == target.ID {
			t.Fatal("ordinary lifecycle discovered unisolated native target")
		}
	}
	input := managedpostgres.Database{ID: uuid.NewString(), AccountID: database.AccountID, Name: "another", State: managedpostgres.StateProvisioning,
		BackendID: receipt.BackendID, BackendFingerprint: strings.Repeat("a", 64), DesiredGeneration: 1,
		Spec: managedpostgres.Spec{Region: "eu-central-1", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone}, CreatedAt: now, UpdatedAt: now}
	if _, _, err := databases.Reserve(ctx, input, 2); !errors.Is(err, managedpostgres.ErrQuotaExceeded) {
		t.Fatalf("adoption released quota: %v", err)
	}
	if _, _, err := databases.Reserve(ctx, input, 3); err != nil {
		t.Fatalf("adoption double counted quota: %v", err)
	}
	if replay, created, err := s.AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID); err != nil || created || replay != target {
		t.Fatalf("adoption replay: %+v %v %v", replay, created, err)
	}
	if replay, err := s.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, snapshot.SourceDatabaseID); err != nil || replay != receipt {
		t.Fatalf("adoption replay moved proof: %+v %v", replay, err)
	}
	lease = cloneForkCompensation(t, s, ctx, lease)
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	proof := state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{TargetProviderResourceID: receipt.TargetProviderResourceID, OperationIDs: []string{"owned-delete"}, Done: true}
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(ctx, lease, snapshot.SourceDatabaseID, proof); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("adopted native target lost snapshot: %v", err)
	}
	if row, err := databases.Get(ctx, database.AccountID, target.ID); err != nil || row.State != managedpostgres.StateProvisioning {
		t.Fatalf("pending deletion retired catalogue: %+v %v", row, err)
	}
	receipt, err = s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID, proof)
	if err != nil || receipt.State != "deleted" {
		t.Fatalf("native retirement: %+v %v", receipt, err)
	}
	if row, err := databases.Get(ctx, database.AccountID, target.ID); err != nil || row.State != managedpostgres.StateDeleted || row.DeletedAt == nil {
		t.Fatalf("fork and catalogue did not retire atomically: %+v %v", row, err)
	}
	input.ID, input.Name = uuid.NewString(), "after-retirement"
	if _, _, err := databases.Reserve(ctx, input, 3); err != nil {
		t.Fatalf("verified adopted retirement retained quota: %v", err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	if replay, err := s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, snapshot.SourceDatabaseID, proof); err != nil || replay != receipt {
		t.Fatalf("retired adoption replay after snapshot cleanup: %+v %v", replay, err)
	}
}

func TestPgClonePostgresSnapshotRestoreAdoptionRejectsForeignNameAndChangedCatalogue(t *testing.T) {
	for _, mode := range []string{"foreign_name", "provider", "data", "generation", "storage", "lineage"} {
		t.Run(mode, func(t *testing.T) {
			s, ctx, pool, lease, snapshot := cloneNativeAdoptionFixture(t)
			if mode == "foreign_name" {
				if _, err := pool.Exec(ctx, `insert into managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint)
                    select $1,account_id,$2,region,postgres_major,service_class,availability,backend_id,backend_fingerprint from managed_postgres_databases where id=$3`, uuid.NewString(), state.ProjectEnvironmentClonePostgresSnapshotRestoreDatabaseName(lease.Operation, snapshot.SourceDatabaseID), snapshot.SourceDatabaseID); err != nil {
					t.Fatal(err)
				}
				if _, _, err := s.AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("adopted foreign catalogue name: %v", err)
				}
				if row, err := s.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, snapshot.SourceDatabaseID); err != nil || row.State != "restored" || row.AdoptedDatabaseID != "" {
					t.Fatalf("foreign row transferred quota: %+v %v", row, err)
				}
				return
			}
			target, _, err := s.AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID)
			if err != nil {
				t.Fatal(err)
			}
			changes := map[string]string{
				"provider": "provider_resource_id='foreign/target'", "data": "data_resource_id='unproven/data'", "generation": "desired_generation=2",
				"storage": "storage_limit_bytes=123456", "lineage": "restore_point_in_time=restore_point_in_time+interval '1 microsecond'",
			}
			// Only fixed test-owned statements are selected; production SQL
			// mutations remain exclusively generated through SQLC.
			if _, err := pool.Exec(ctx, "update managed_postgres_databases set "+changes[mode]+" where id=$1", target.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("accepted changed %s: %v", mode, err)
			}
			if _, _, err := s.AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("re-adopted changed %s: %v", mode, err)
			}
		})
	}
}

func TestPgClonePostgresSnapshotRestoreAdoptionKeepsCaptureSeparateFromStageTarget(t *testing.T) {
	s, ctx, pool, lease, snapshot := cloneNativeAdoptionFixture(t)
	capture, _, err := s.AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	op := lease.Operation
	targetID := uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,
        backend_id,backend_fingerprint,restore_source_database_id,restore_source_resource_id,restore_point_in_time,environment_clone_operation_id)
        select $1,account_id,$2,region,postgres_major,service_class,availability,backend_id,backend_fingerprint,id,data_resource_id,$3,$4
        from managed_postgres_databases where id=$5`, targetID, state.ProjectEnvironmentCloneDatabaseName(op, snapshot.SourceDatabaseID), snapshot.CapturePoint, op.ID, snapshot.SourceDatabaseID); err != nil {
		t.Fatalf("capture occupied final target reservation: %v", err)
	}
	if receipt, err := s.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, snapshot.SourceDatabaseID); err != nil || receipt.AdoptedDatabaseID != capture.ID {
		t.Fatalf("final target replaced capture ownership: %+v %v", receipt, err)
	}
	if _, err := pool.Exec(ctx, "insert into project_environments(account_id,project_id,slug) values($1,$2,$3)", op.AccountID, op.ProjectID, op.TargetEnvironment); err != nil {
		t.Fatal(err)
	}
	resources := append([]state.ProjectEnvironmentCloneResource{}, op.Resources...)
	resources[0].TargetID, resources[0].Status = capture.ID, "ready"
	raw, err := json.Marshal(resources)
	if err != nil {
		t.Fatal(err)
	}
	// Even a forged or future broad operation publication receipt cannot
	// turn an intermediate provider-project fork into a customer database.
	if _, err := pool.Exec(ctx, "update project_environment_clone_operations set status='ready',resources=$2 where id=$1", op.ID, raw); err != nil {
		t.Fatal(err)
	}
	databases, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := databases.GetCustomerDatabase(ctx, op.AccountID, capture.ID); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("capture resource became customer visible: %v", err)
	}
	visible, err := databases.ListCustomerDatabases(ctx, op.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	for _, database := range visible {
		if database.ID == capture.ID {
			t.Fatal("published operation listed a capture database")
		}
	}
}

func TestPgClonePostgresSnapshotRestoreAdoptionChecksFreshLeaseAfterOwnedLocks(t *testing.T) {
	for _, lockKind := range []string{"account", "fork", "catalogue"} {
		t.Run(lockKind, func(t *testing.T) {
			s, ctx, pool, lease, snapshot := cloneNativeAdoptionFixture(t)
			var target state.ProjectEnvironmentCloneDatabaseTarget
			if lockKind == "catalogue" {
				var err error
				target, _, err = s.AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID)
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
			needle := "ReadProjectEnvironmentClonePostgresSnapshotRestore"
			switch lockKind {
			case "account":
				needle = "LockProjectEnvironmentCloneDatabaseAccount"
				_, err = lock.Exec(ctx, "select id from accounts where id=$1 for update", lease.Operation.AccountID)
			case "fork":
				_, err = lock.Exec(ctx, "select operation_id from project_environment_clone_postgres_snapshot_restores where operation_id=$1 for update", lease.Operation.ID)
			case "catalogue":
				needle = "LockManagedPostgresLifecycleDatabase"
				_, err = lock.Exec(ctx, "select id from managed_postgres_databases where id=$1 for update", target.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, _, err := s.AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID)
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
					t.Fatal("adoption did not reach owned lock")
				}
				time.Sleep(5 * time.Millisecond)
			}
			time.Sleep(time.Until(lease.ExpiresAt) + 40*time.Millisecond)
			if err := lock.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, state.ErrConflict) {
				t.Fatalf("expired adoption after %s lock: %v", lockKind, err)
			}
			var count int
			if err := pool.QueryRow(ctx, "select count(*) from managed_postgres_databases where environment_clone_operation_id=$1", lease.Operation.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if lockKind == "catalogue" {
				want = 1
			}
			if count != want {
				t.Fatalf("expired adoption changed ownership: got %d want %d", count, want)
			}
		})
	}
}

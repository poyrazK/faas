//go:build !no_pg

// adr:531
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/state"
)

func clonePostgresArchiveFixture(t *testing.T) (*state.PgStore, context.Context, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresArchiveRequest) {
	t.Helper()
	s, ctx, pool, lease, sealed, _ := clonePostgresInventoryFixture(t)
	if _, _, err := s.RecordProjectEnvironmentClonePostgresInventory(ctx, lease, sealed.Scope.SourceDatabaseID, sealed); err != nil {
		t.Fatal(err)
	}
	var oid uint32
	if err := pool.QueryRow(ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	request := state.ProjectEnvironmentClonePostgresArchiveRequest{Scope: sealed.Scope, DatabaseOID: oid, InventoryFingerprint: sealed.Fingerprint, KeyID: sealed.KeyID,
		StorageID: "private-artifacts", StorageFingerprint: strings.Repeat("d", 64), ReservedBytes: 8192}
	return s, ctx, pool, lease, request
}

func archiveLimits() state.ProjectEnvironmentClonePostgresArchiveLimits {
	return state.ProjectEnvironmentClonePostgresArchiveLimits{Count: 3, Bytes: 32768}
}

func archiveTestReceipt(r state.ProjectEnvironmentClonePostgresArchiveRequest) copyarchive.Receipt {
	return copyarchive.Receipt{Scope: r.Scope, InventoryFingerprint: r.InventoryFingerprint, SourceDatabaseOID: r.DatabaseOID, PlainBytes: 1024, CiphertextBytes: 4096, CiphertextSHA256: strings.Repeat("e", 64)}
}

func TestPgClonePostgresArchiveSingleOwnerDispatchAndHandoff(t *testing.T) {
	s, ctx, pool, l, request := clonePostgresArchiveFixture(t)
	type result struct {
		a       state.ProjectEnvironmentClonePostgresArchive
		created bool
		err     error
	}
	var wg sync.WaitGroup
	results := make(chan result, 6)
	for range 6 {
		wg.Go(func() {
			a, created, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, request, archiveLimits())
			results <- result{a, created, err}
		})
	}
	wg.Wait()
	close(results)
	var first state.ProjectEnvironmentClonePostgresArchive
	creations := 0
	for got := range results {
		if got.err != nil || got.a.State != "reserved" || !got.a.UploadStartedAt.IsZero() || !got.a.RetainedAt.IsZero() || got.a.Receipt != (copyarchive.Receipt{}) {
			t.Fatalf("reservation: %v", got.err)
		}
		if first.OwnerID == "" {
			first = got.a
		}
		if got.a.OwnerID != first.OwnerID || got.a.StorageKey != first.StorageKey || !got.a.CreatedAt.Equal(first.CreatedAt) {
			t.Fatal("concurrent reservation replaced owner")
		}
		if got.created {
			creations++
		}
	}
	if creations != 1 || first.StorageKey != "postgres-copies/"+l.Operation.ID+"/"+first.OwnerID+".age" {
		t.Fatal("invalid owned key or duplicate owner")
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresArchive(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID, archiveTestReceipt(request)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("undispatched archive retained: %v", err)
	}
	a, dispatch, err := s.ClaimProjectEnvironmentClonePostgresArchiveUpload(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID)
	if err != nil || !dispatch || a.State != "uploading" || a.UploadStartedAt.IsZero() {
		t.Fatalf("first dispatch: %v %v", dispatch, err)
	}
	old := l
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, l, 0); err != nil {
		t.Fatal(err)
	}
	l, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if recovered, dispatch, err := s.ClaimProjectEnvironmentClonePostgresArchiveUpload(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID); err != nil || dispatch || recovered.OwnerID != a.OwnerID || !recovered.UploadStartedAt.Equal(a.UploadStartedAt) {
		t.Fatalf("handoff repeated uncertain upload: %v %v", dispatch, err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresArchiveForLease(ctx, old, request.Scope.SourceDatabaseID, request.DatabaseOID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale private read: %v", err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresArchive(ctx, old, request.Scope.SourceDatabaseID, request.DatabaseOID, archiveTestReceipt(request)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale receipt write: %v", err)
	}
	retained, err := s.RecordProjectEnvironmentClonePostgresArchive(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID, archiveTestReceipt(request))
	if err != nil || retained.State != "retained" || retained.RetainedAt.IsZero() || !copyarchive.SameReceipt(retained.Receipt, archiveTestReceipt(request)) {
		t.Fatalf("verified receipt: %v", err)
	}
	if replay, err := s.RecordProjectEnvironmentClonePostgresArchive(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID, archiveTestReceipt(request)); err != nil || !replay.RetainedAt.Equal(retained.RetainedAt) {
		t.Fatalf("committed reply loss: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE managed_postgres_databases SET storage_limit_bytes=123456,restore_window_seconds=0 WHERE id=$1", request.Scope.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	if replay, err := s.ProjectEnvironmentClonePostgresArchiveForLease(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID); err != nil || !copyarchive.SameReceipt(replay.Receipt, retained.Receipt) {
		t.Fatalf("live desired edit rebased archive: %v", err)
	}
}

func TestPgClonePostgresArchivePinsAndByteReservationsCannotBeReplaced(t *testing.T) {
	s, ctx, pool, l, request := clonePostgresArchiveFixture(t)
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, request, archiveLimits()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*state.ProjectEnvironmentClonePostgresArchiveRequest)
	}{
		{"capture_scope", func(r *state.ProjectEnvironmentClonePostgresArchiveRequest) {
			r.Scope.CaptureDatabaseID = uuid.NewString()
		}},
		{"inventory", func(r *state.ProjectEnvironmentClonePostgresArchiveRequest) {
			r.InventoryFingerprint = strings.Repeat("f", 64)
		}},
		{"storage_id", func(r *state.ProjectEnvironmentClonePostgresArchiveRequest) { r.StorageID = "other" }},
		{"storage_fingerprint", func(r *state.ProjectEnvironmentClonePostgresArchiveRequest) {
			r.StorageFingerprint = strings.Repeat("f", 64)
		}},
		{"reservation", func(r *state.ProjectEnvironmentClonePostgresArchiveRequest) { r.ReservedBytes++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := request
			tc.edit(&changed)
			if _, _, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, changed, archiveLimits()); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("replaced archive input: %v", err)
			}
		})
	}
	if _, dispatch, err := s.ClaimProjectEnvironmentClonePostgresArchiveUpload(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID); err != nil || !dispatch {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*copyarchive.Receipt)
	}{
		{"source", func(r *copyarchive.Receipt) { r.SourceDatabaseOID++ }},
		{"scope", func(r *copyarchive.Receipt) { r.Scope.OperationID = uuid.NewString() }},
		{"inventory", func(r *copyarchive.Receipt) { r.InventoryFingerprint = strings.Repeat("f", 64) }},
		{"budget", func(r *copyarchive.Receipt) { r.CiphertextBytes = request.ReservedBytes + 1 }},
		{"unencrypted_length", func(r *copyarchive.Receipt) { r.CiphertextBytes = r.PlainBytes }},
		{"hash", func(r *copyarchive.Receipt) { r.CiphertextSHA256 = "wrong" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := archiveTestReceipt(request)
			tc.edit(&changed)
			if _, err := s.RecordProjectEnvironmentClonePostgresArchive(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID, changed); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("accepted invalid receipt: %v", err)
			}
		})
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresArchive(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID, archiveTestReceipt(request)); err != nil {
		t.Fatal(err)
	}
	changed := archiveTestReceipt(request)
	changed.CiphertextSHA256 = strings.Repeat("f", 64)
	if _, err := s.RecordProjectEnvironmentClonePostgresArchive(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("overwrote retained receipt: %v", err)
	}
	// Retained bytes do not silently release the original storage reservation.
	next := request
	next.DatabaseOID++
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, next, state.ProjectEnvironmentClonePostgresArchiveLimits{Count: 1, Bytes: 32768}); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("retained count hold released: %v", err)
	}
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, next, state.ProjectEnvironmentClonePostgresArchiveLimits{Count: 3, Bytes: request.ReservedBytes}); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("retained byte hold released: %v", err)
	}
	if _, created, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, request, state.ProjectEnvironmentClonePostgresArchiveLimits{Count: 1, Bytes: 1}); err != nil || created {
		t.Fatalf("existing ownership blocked by reduced admission: %v", err)
	}
	var held int64
	if err := pool.QueryRow(ctx, "SELECT sum(reserved_bytes) FROM project_environment_clone_postgres_archives WHERE account_id=$1", l.Operation.AccountID).Scan(&held); err != nil || held != request.ReservedBytes {
		t.Fatalf("archive quota: %d %v", held, err)
	}
}

func TestPgClonePostgresArchiveRequiresInventoryAndAuthenticatesStoredScope(t *testing.T) {
	s, ctx, pool, l, request := clonePostgresArchiveFixture(t)
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, request, state.ProjectEnvironmentClonePostgresArchiveLimits{Count: api.PostgresCopyArchivesPerAccountMax + 1, Bytes: 32768}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unbounded reservation: %v", err)
	}
	oversize := request
	oversize.ReservedBytes = api.PostgresCopyArchiveCiphertextMaxBytes + 1
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, oversize, archiveLimits()); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unbounded byte reservation: %v", err)
	}
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, request, archiveLimits()); err != nil {
		t.Fatal(err)
	}
	changed := request.Scope
	changed.BackendFingerprint = strings.Repeat("f", 64)
	raw, _ := json.Marshal(changed)
	if _, err := pool.Exec(ctx, "UPDATE project_environment_clone_postgres_archives SET scope=$1 WHERE operation_id=$2", raw, l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresArchiveForLease(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stored scope rebound: %v", err)
	}
	// Restore the stored scope, then remove the inventory in a fresh fixture:
	// no private artifact may exist without its authenticated metadata input.
	s, ctx, pool, l, request = clonePostgresArchiveFixture(t)
	if _, err := pool.Exec(ctx, "DELETE FROM project_environment_clone_postgres_inventories WHERE operation_id=$1", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, request, archiveLimits()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("archive without inventory: %v", err)
	}
}

func TestPgClonePostgresArchiveRechecksLeaseAfterArtifactLock(t *testing.T) {
	s, ctx, pool, l, request := clonePostgresArchiveFixture(t)
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, request, archiveLimits()); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "UPDATE project_environment_clone_operations SET lease_until=clock_timestamp()+interval '300 milliseconds' WHERE id=$1 RETURNING lease_until", l.Operation.ID).Scan(&l.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "SELECT owner_id FROM project_environment_clone_postgres_archives WHERE operation_id=$1 FOR UPDATE", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := s.ClaimProjectEnvironmentClonePostgresArchiveUpload(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID)
		done <- err
	}()
	time.Sleep(500 * time.Millisecond)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired worker dispatched after lock wait: %v", err)
	}
	var dispatched bool
	if err := pool.QueryRow(ctx, "SELECT upload_started_at IS NOT NULL FROM project_environment_clone_postgres_archives WHERE operation_id=$1", l.Operation.ID).Scan(&dispatched); err != nil || dispatched {
		t.Fatalf("expired dispatch changed row: %v %v", dispatched, err)
	}
}

func TestPgClonePostgresArchiveRecoversAfterOwnedCaptureRetirement(t *testing.T) {
	s, ctx, _, l, request := clonePostgresArchiveFixture(t)
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, request, archiveLimits()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClaimProjectEnvironmentClonePostgresArchiveUpload(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID); err != nil {
		t.Fatal(err)
	}
	retained, err := s.RecordProjectEnvironmentClonePostgresArchive(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID, archiveTestReceipt(request))
	if err != nil {
		t.Fatal(err)
	}
	l = cloneForkCompensation(t, s, ctx, l)
	capture, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, l, request.Scope.SourceDatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	proof := state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{TargetProviderResourceID: capture.TargetProviderResourceID, OperationIDs: []string{"delete-owned-capture"}, Done: true}
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(ctx, l, request.Scope.SourceDatabaseID, proof); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, l, request.Scope.SourceDatabaseID, proof); err != nil {
		t.Fatal(err)
	}
	if recovered, err := s.ProjectEnvironmentClonePostgresArchiveForLease(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID); err != nil || !copyarchive.SameReceipt(recovered.Receipt, retained.Receipt) {
		t.Fatalf("capture retirement lost retained artifact: %v", err)
	}
	if _, _, err := s.ClaimProjectEnvironmentClonePostgresArchiveUpload(ctx, l, request.Scope.SourceDatabaseID, request.DatabaseOID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("compensation dispatched upload: %v", err)
	}
}

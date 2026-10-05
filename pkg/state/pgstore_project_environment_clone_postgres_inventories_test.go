//go:build !no_pg

// adr: 590
package state_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func clonePostgresInventoryFixture(t *testing.T) (*state.PgStore, context.Context, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, copyinventory.Sealed, *age.X25519Identity) {
	t.Helper()
	s, ctx, pool, lease, snapshot := cloneNativeAdoptionFixture(t, 16)
	if _, _, err := s.AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx, lease, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	scope, err := s.ProjectEnvironmentClonePostgresInventoryScopeForLease(ctx, lease, snapshot.SourceDatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var cfg copyinventory.Config
	// This reads the private fixture cluster to exercise the actual inventory
	// codec/encryption; it does not qualify a provider capture SQL connection.
	if err := conn.QueryRow(ctx, `select current_setting('server_version_num')::integer/10000,current_database(),current_user,
        (select oid::bigint from pg_database where datname=current_database()),(select oid::bigint from pg_roles where rolname=current_user)`).Scan(
		&cfg.PostgresMajor, &cfg.DatabaseName, &cfg.RoleName, &cfg.DatabaseOID, &cfg.RoleOID); err != nil {
		t.Fatal(err)
	}
	cfg.FingerprintKey[0] = 71
	inventory, err := copyinventory.Read(ctx, conn.Conn(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := age.GenerateX25519Identity()
	sealed, err := copyinventory.SealInventory(identity.Recipient(), scope, cfg, inventory)
	if err != nil {
		t.Fatal(err)
	}
	return s, ctx, pool, lease, sealed, identity
}

func TestPgClonePostgresSnapshotInventoryWriteOnceAndHandoff(t *testing.T) {
	s, ctx, pool, lease, sealed, identity := clonePostgresInventoryFixture(t)
	for _, at := range []time.Time{sealed.Scope.CapturePoint, sealed.Scope.SnapshotCreatedAt, sealed.Scope.CaptureCreatedAt} {
		if at.Location() != time.UTC {
			t.Fatal("database scope did not canonicalize its timestamp locations")
		}
	}
	id := sealed.Scope.SourceDatabaseID
	if _, err := s.ProjectEnvironmentClonePostgresInventoryForLease(ctx, lease, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("invented capture: %v", err)
	}
	receipt, created, err := s.RecordProjectEnvironmentClonePostgresInventory(ctx, lease, id, sealed)
	if err != nil || !created || receipt.CapturedAt.IsZero() || !bytes.Equal(receipt.Sealed.Ciphertext, sealed.Ciphertext) {
		t.Fatalf("first encrypted capture: %v %v", created, err)
	}
	if replay, created, err := s.RecordProjectEnvironmentClonePostgresInventory(ctx, lease, id, sealed); err != nil || created || !replay.CapturedAt.Equal(receipt.CapturedAt) {
		t.Fatalf("reply-loss replay: %v %v", created, err)
	}
	inventory, err := copyinventory.OpenInventory([]*age.X25519Identity{identity}, sealed.Scope, receipt.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	old := lease
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	lease, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresInventoryForLease(ctx, old, id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker read private capture: %v", err)
	}
	if _, _, err := s.RecordProjectEnvironmentClonePostgresInventory(ctx, old, id, sealed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker wrote private capture: %v", err)
	}
	// Changing live desired source configuration cannot rebase committed input.
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set storage_limit_bytes=123456,restore_window_seconds=0 where id=$1", id); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.ProjectEnvironmentClonePostgresInventoryForLease(ctx, lease, id)
	if err != nil || !bytes.Equal(recovered.Sealed.Ciphertext, receipt.Sealed.Ciphertext) || !recovered.CapturedAt.Equal(receipt.CapturedAt) {
		t.Fatalf("handoff changed committed capture: %v", err)
	}
	value, err := copyinventory.OpenInventory([]*age.X25519Identity{identity}, sealed.Scope, recovered.Sealed)
	if err != nil || value.Summary() != inventory.Summary() {
		t.Fatalf("handoff open: %v", err)
	}
	// Randomized re-encryption of the same valid payload is not an overwrite.
	namespace, raw, err := secretbox.OpenBytes(identity, sealed.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	replacement := sealed
	replacement.Ciphertext, err = secretbox.SealBytes(identity.Recipient(), namespace, raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(replacement.Ciphertext)
	replacement.CiphertextSHA256 = hex.EncodeToString(hash[:])
	if _, _, err := s.RecordProjectEnvironmentClonePostgresInventory(ctx, lease, id, replacement); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("valid randomized ciphertext overwrote committed capture: %v", err)
	}
	replacement = sealed
	replacement.Fingerprint = string(bytes.Repeat([]byte("c"), 64))
	if _, _, err := s.RecordProjectEnvironmentClonePostgresInventory(ctx, lease, id, replacement); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("committed metadata overwritten: %v", err)
	}
	op := lease.Operation
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, op.Resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("inventory forged complete data proof: %v", err)
	}
}

func TestPgClonePostgresSnapshotInventoryRejectsScopeDriftAndCatalogueReplacement(t *testing.T) {
	s, ctx, pool, lease, sealed, _ := clonePostgresInventoryFixture(t)
	id := sealed.Scope.SourceDatabaseID
	oversized := sealed
	oversized.Ciphertext = make([]byte, api.PostgresCopyCiphertextMaxBytes+1)
	if _, _, err := s.RecordProjectEnvironmentClonePostgresInventory(ctx, lease, id, oversized); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("oversized private metadata lost quota classification: %v", err)
	}
	for _, mode := range []string{"operation", "account", "project", "database", "capture", "backend", "point", "major"} {
		t.Run(mode, func(t *testing.T) {
			changed := sealed
			switch mode {
			case "operation":
				changed.Scope.OperationID = uuid.NewString()
			case "account":
				changed.Scope.AccountID = uuid.NewString()
			case "project":
				changed.Scope.ProjectID = uuid.NewString()
			case "database":
				changed.Scope.SourceDatabaseID = uuid.NewString()
			case "capture":
				changed.Scope.CaptureDatabaseID = uuid.NewString()
			case "backend":
				changed.Scope.BackendID = "other-backend"
			case "point":
				changed.Scope.CapturePoint = changed.Scope.CapturePoint.Add(-time.Second)
			case "major":
				changed.Scope.PostgresMajor++
			}
			if _, _, err := s.RecordProjectEnvironmentClonePostgresInventory(ctx, lease, id, changed); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("foreign scope accepted: %v", err)
			}
		})
	}
	if _, _, err := s.RecordProjectEnvironmentClonePostgresInventory(ctx, lease, id, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set provider_resource_id='provider/replacement' where id=$1", sealed.Scope.CaptureDatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresInventoryForLease(ctx, lease, id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("replacement catalogue selected original capture: %v", err)
	}
}

func TestPgClonePostgresSnapshotInventorySurvivesAuthenticatedInputCleanup(t *testing.T) {
	s, ctx, pool, lease, sealed, identity := clonePostgresInventoryFixture(t)
	id := sealed.Scope.SourceDatabaseID
	original, _, err := s.RecordProjectEnvironmentClonePostgresInventory(ctx, lease, id, sealed)
	if err != nil {
		t.Fatal(err)
	}
	lease = cloneForkCompensation(t, s, ctx, lease)
	if _, _, err := s.RecordProjectEnvironmentClonePostgresInventory(ctx, lease, id, sealed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("compensation created/replaced capture: %v", err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, id); err != nil {
		t.Fatal(err)
	}
	proof := state.ProjectEnvironmentClonePostgresSnapshotRestoreDeletion{TargetProviderResourceID: sealed.Scope.CaptureProviderResourceID, OperationIDs: []string{"owned-delete"}, Done: true}
	if _, err := s.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(ctx, lease, id, proof); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, lease, id, proof); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresSnapshotCleanup(ctx, lease, id); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.ProjectEnvironmentClonePostgresInventoryForLease(ctx, lease, id)
	if err != nil || !recovered.CapturedAt.Equal(original.CapturedAt) || !bytes.Equal(recovered.Sealed.Ciphertext, original.Sealed.Ciphertext) {
		t.Fatalf("provider cleanup discarded private metadata: %v", err)
	}
	if _, err := copyinventory.OpenInventory([]*age.X25519Identity{identity}, sealed.Scope, recovered.Sealed); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "alter table project_environment_clone_postgres_inventories add constraint postgres_inventories_down_no_capture check(false)"); err == nil {
		t.Fatal("migration downgrade discarded committed capture")
	}
}

func TestPgClonePostgresSnapshotInventoryRechecksLeaseAfterReceiptLock(t *testing.T) {
	s, ctx, pool, lease, sealed, _ := clonePostgresInventoryFixture(t)
	id := sealed.Scope.SourceDatabaseID
	if _, _, err := s.RecordProjectEnvironmentClonePostgresInventory(ctx, lease, id, sealed); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "update project_environment_clone_operations set lease_until=clock_timestamp()+interval '300 milliseconds' where id=$1 returning lease_until", lease.Operation.ID).Scan(&lease.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := lock.Exec(ctx, "select operation_id from project_environment_clone_postgres_inventories where operation_id=$1 for update", lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.ProjectEnvironmentClonePostgresInventoryForLease(ctx, lease, id); done <- err }()
	time.Sleep(500 * time.Millisecond)
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired lease read after lock wait: %v", err)
	}
}

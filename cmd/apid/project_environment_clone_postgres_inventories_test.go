//go:build !no_pg

// adr:531
package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneInventoryFailureStore struct {
	*state.PgStore
	loseReply bool
}

func (s *cloneInventoryFailureStore) RecordProjectEnvironmentClonePostgresInventory(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, sealed copyinventory.Sealed) (state.ProjectEnvironmentClonePostgresInventory, bool, error) {
	r, created, err := s.PgStore.RecordProjectEnvironmentClonePostgresInventory(ctx, l, id, sealed)
	if err == nil && s.loseReply {
		s.loseReply = false
		return state.ProjectEnvironmentClonePostgresInventory{}, false, managedpostgres.ErrUnavailable
	}
	return r, created, err
}

func cloneInventoryWorkerFixture(t *testing.T) (cloneCoordinatorFixture, *cloneInventoryFailureStore, string, *age.X25519Identity, clonePostgresInventoryRead, *int) {
	t.Helper()
	f, original, _, sourceID := cloneSnapshotRestoreWorkerFixture(t, 16)
	var err error
	f.lease, _, err = f.srv.restoreProjectEnvironmentClonePostgresSnapshots(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	store := &cloneInventoryFailureStore{PgStore: original.PgStore}
	f.srv.store = store
	identity, _ := age.GenerateX25519Identity()
	oldRecipient, oldIdentities := setSecretRecipient, mfaIdentities
	setSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { setSecretRecipient, mfaIdentities = oldRecipient, oldIdentities })
	reads := new(int)
	// Real SQL metadata on the private test cluster plus synthetic native
	// receipts exercises persistence/recovery, not live-provider placement.
	read := func(ctx context.Context, scope copyinventory.Scope, key [32]byte) (copyinventory.Config, copyinventory.Inventory, error) {
		(*reads)++
		if deadline, ok := ctx.Deadline(); !ok || deadline.After(f.lease.ExpiresAt) || scope.SourceDatabaseID != sourceID || scope.PostgresMajor != 16 {
			return copyinventory.Config{}, copyinventory.Inventory{}, managedpostgres.ErrConflict
		}
		conn, err := f.pool.Acquire(ctx)
		if err != nil {
			return copyinventory.Config{}, copyinventory.Inventory{}, err
		}
		defer conn.Release()
		cfg := copyinventory.Config{FingerprintKey: key}
		if err := conn.QueryRow(ctx, `select current_setting('server_version_num')::integer/10000,current_database(),current_user,
            (select oid::bigint from pg_database where datname=current_database()),(select oid::bigint from pg_roles where rolname=current_user)`).Scan(
			&cfg.PostgresMajor, &cfg.DatabaseName, &cfg.RoleName, &cfg.DatabaseOID, &cfg.RoleOID); err != nil {
			return cfg, copyinventory.Inventory{}, err
		}
		inventory, err := copyinventory.Read(ctx, conn.Conn(), cfg)
		return cfg, inventory, err
	}
	return f, store, sourceID, identity, read, reads
}

func TestPGClonePostgresSnapshotInventoryWorkerRecoversCommittedReplyWithKeyRotation(t *testing.T) {
	f, store, sourceID, previous, read, reads := cloneInventoryWorkerFixture(t)
	store.loseReply = true
	if _, err := f.srv.projectEnvironmentClonePostgresInventory(t.Context(), f.lease, sourceID, read); !errors.Is(err, managedpostgres.ErrUnavailable) || *reads != 1 {
		t.Fatalf("lost committed response: reads=%d err=%v", *reads, err)
	}
	original, err := store.ProjectEnvironmentClonePostgresInventoryForLease(t.Context(), f.lease, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := age.GenerateX25519Identity()
	setSecretRecipient = func() *age.X25519Recipient { return current.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current, nil, previous} }
	inventory, err := f.srv.projectEnvironmentClonePostgresInventory(t.Context(), f.lease, sourceID, nil)
	if err != nil || *reads != 1 || inventory.Summary().Fingerprint != original.Sealed.Fingerprint {
		t.Fatalf("handoff recaptured SQL metadata: reads=%d err=%v", *reads, err)
	}
	recovered, err := store.ProjectEnvironmentClonePostgresInventoryForLease(t.Context(), f.lease, sourceID)
	if err != nil || !bytes.Equal(recovered.Sealed.Ciphertext, original.Sealed.Ciphertext) || !recovered.CapturedAt.Equal(original.CapturedAt) {
		t.Fatalf("rotation replaced committed ciphertext: %v", err)
	}
	if f.lease.Operation.Resources[0].TargetID != "" || f.lease.Operation.Resources[0].Status != "captured" {
		t.Fatal("metadata receipt supplied dataset readiness")
	}
}

func TestPGClonePostgresSnapshotInventoryWorkerDoesNotRecaptureUnreadableReceipt(t *testing.T) {
	f, _, sourceID, identity, read, reads := cloneInventoryWorkerFixture(t)
	if _, err := f.srv.projectEnvironmentClonePostgresInventory(t.Context(), f.lease, sourceID, read); err != nil {
		t.Fatal(err)
	}
	other, _ := age.GenerateX25519Identity()
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{other} }
	if _, err := f.srv.projectEnvironmentClonePostgresInventory(t.Context(), f.lease, sourceID, read); !errors.Is(err, managedpostgres.ErrUnavailable) || *reads != 1 {
		t.Fatalf("missing decryption key recaptured: reads=%d err=%v", *reads, err)
	}
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	if _, err := f.pool.Exec(t.Context(), "update project_environment_clone_postgres_inventories set ciphertext=decode('00','hex') where operation_id=$1", f.lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.projectEnvironmentClonePostgresInventory(t.Context(), f.lease, sourceID, read); !errors.Is(err, state.ErrConflict) || *reads != 1 {
		t.Fatalf("tampered receipt recaptured: reads=%d err=%v", *reads, err)
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_postgres_inventories where operation_id=$1", f.lease.Operation.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unreadable receipt lost ownership: %d %v", count, err)
	}
}

func TestPGClonePostgresSnapshotInventoryWorkerRejectsMissingKeysAndFailedReads(t *testing.T) {
	f, store, sourceID, identity, read, reads := cloneInventoryWorkerFixture(t)
	setSecretRecipient = nil
	if _, err := f.srv.projectEnvironmentClonePostgresInventory(t.Context(), f.lease, sourceID, read); !errors.Is(err, managedpostgres.ErrUnavailable) || *reads != 0 {
		t.Fatalf("read without sealing key: %d %v", *reads, err)
	}
	setSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return nil }
	if _, err := f.srv.projectEnvironmentClonePostgresInventory(t.Context(), f.lease, sourceID, read); !errors.Is(err, managedpostgres.ErrUnavailable) || *reads != 0 {
		t.Fatalf("read with unopenable current key: %d %v", *reads, err)
	}
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	if _, err := f.srv.projectEnvironmentClonePostgresInventory(t.Context(), f.lease, sourceID, func(context.Context, copyinventory.Scope, [32]byte) (copyinventory.Config, copyinventory.Inventory, error) {
		return copyinventory.Config{}, copyinventory.Inventory{}, managedpostgres.ErrUnavailable
	}); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("failed read: %v", err)
	}
	if _, err := store.ProjectEnvironmentClonePostgresInventoryForLease(t.Context(), f.lease, sourceID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed read committed metadata: %v", err)
	}
}

func TestPGClonePostgresSnapshotInventoryWorkerRejectsHandoffAndKeySubstitutionDuringRead(t *testing.T) {
	for _, mode := range []string{"handoff", "key", "capture", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f, store, sourceID, _, read, reads := cloneInventoryWorkerFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			_, err := f.srv.projectEnvironmentClonePostgresInventory(ctx, f.lease, sourceID, func(ctx context.Context, scope copyinventory.Scope, key [32]byte) (copyinventory.Config, copyinventory.Inventory, error) {
				cfg, inventory, err := read(ctx, scope, key)
				if err != nil {
					return cfg, inventory, err
				}
				switch mode {
				case "handoff":
					if err := store.ReleaseProjectEnvironmentCloneLease(ctx, f.lease, 0); err != nil {
						return cfg, inventory, err
					}
					var err error
					f.lease, err = store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
					if err != nil {
						return cfg, inventory, err
					}
				case "key":
					cfg.FingerprintKey[0] ^= 1
				case "capture":
					_, err := f.pool.Exec(ctx, "update managed_postgres_databases set provider_resource_id='provider/replacement' where id=$1", scope.CaptureDatabaseID)
					if err != nil {
						return cfg, inventory, err
					}
				case "cancel":
					cancel()
				}
				return cfg, inventory, nil
			})
			if err == nil || *reads != 1 {
				t.Fatalf("changed authority committed capture: %d %v", *reads, err)
			}
			var count int
			if err := f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_postgres_inventories where operation_id=$1", f.lease.Operation.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed read committed private metadata: %d %v", count, err)
			}
		})
	}
}

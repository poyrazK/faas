//go:build !no_pg

// adr:531
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type archiveWorkerFailureStore struct {
	*state.PgStore
	loseReserve, loseClaim, loseRecord bool
}

func (s *archiveWorkerFailureStore) ReserveProjectEnvironmentClonePostgresArchive(ctx context.Context, l state.ProjectEnvironmentCloneLease, r state.ProjectEnvironmentClonePostgresArchiveRequest, limit state.ProjectEnvironmentClonePostgresArchiveLimits) (state.ProjectEnvironmentClonePostgresArchive, bool, error) {
	a, created, err := s.PgStore.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, r, limit)
	if err == nil && s.loseReserve {
		s.loseReserve = false
		return a, false, managedpostgres.ErrUnavailable
	}
	return a, created, err
}
func (s *archiveWorkerFailureStore) ClaimProjectEnvironmentClonePostgresArchiveUpload(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, oid uint32) (state.ProjectEnvironmentClonePostgresArchive, bool, error) {
	a, dispatch, err := s.PgStore.ClaimProjectEnvironmentClonePostgresArchiveUpload(ctx, l, id, oid)
	if err == nil && s.loseClaim {
		s.loseClaim = false
		return a, false, managedpostgres.ErrUnavailable
	}
	return a, dispatch, err
}
func (s *archiveWorkerFailureStore) RecordProjectEnvironmentClonePostgresArchive(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, oid uint32, r copyarchive.Receipt) (state.ProjectEnvironmentClonePostgresArchive, error) {
	a, err := s.PgStore.RecordProjectEnvironmentClonePostgresArchive(ctx, l, id, oid, r)
	if err == nil && s.loseRecord {
		s.loseRecord = false
		return a, managedpostgres.ErrUnavailable
	}
	return a, err
}

type archiveWorkerStorage struct {
	storage.StorageBackend
	puts, gets int
	losePut    bool
}

func (b *archiveWorkerStorage) Put(ctx context.Context, key string, r io.Reader) error {
	b.puts++
	err := b.StorageBackend.Put(ctx, key, r)
	if err == nil && b.losePut {
		b.losePut = false
		return errors.New("private lost upload response")
	}
	return err
}
func (b *archiveWorkerStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	b.gets++
	return b.StorageBackend.Get(ctx, key)
}

func cloneArchiveWorkerFixture(t *testing.T) (cloneCoordinatorFixture, *archiveWorkerFailureStore, copyinventory.ExportPlan, uint32, *age.X25519Identity, clonePostgresArchiveStorage, *archiveWorkerStorage, clonePostgresArchiveProduce, *int) {
	t.Helper()
	f, original, sourceID, identity, read, _ := cloneInventoryWorkerFixture(t)
	inventory, err := f.srv.projectEnvironmentClonePostgresInventory(t.Context(), f.lease, sourceID, read)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := original.ProjectEnvironmentClonePostgresInventoryForLease(t.Context(), f.lease, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := inventory.PlanExports(sealed.Sealed.Scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	req, err := plan.RequirementsForWorker()
	if err != nil {
		t.Fatal(err)
	}
	var oid uint32
	for _, d := range req {
		if d.AuthenticatedReaderDatabase {
			oid = d.Database.OID
		}
	}
	if oid == 0 {
		t.Fatal("missing reader database")
	}
	store := &archiveWorkerFailureStore{PgStore: original.PgStore}
	f.srv.store = store
	local, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := &archiveWorkerStorage{StorageBackend: local}
	artifact := clonePostgresArchiveStorage{ID: "private-artifacts", Fingerprint: strings.Repeat("e", 64), Backend: b}
	tool := filepath.Join(t.TempDir(), "pg_dump")
	// Synthetic dump output exercises ownership/crypto/SQL composition. The
	// copyarchive contracts separately verify actual pg_dump/pg_restore data.
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf 'pg_dump (PostgreSQL) 16.1\\n'; exit 0; fi\nprintf PGDMPfixturearchive\n"), 0700); err != nil {
		t.Fatal(err)
	}
	calls := new(int)
	produce := func(ctx context.Context, d copyinventory.DatabaseExport, key *age.X25519Recipient, w io.Writer) (copyarchive.Receipt, error) {
		(*calls)++
		if deadline, ok := ctx.Deadline(); !ok || deadline.After(f.lease.ExpiresAt) || key.String() != identity.Recipient().String() {
			return copyarchive.Receipt{}, managedpostgres.ErrConflict
		}
		cfg := f.pool.Config().ConnConfig.Copy()
		cfg.RuntimeParams = map[string]string{"default_transaction_read_only": "on", "search_path": "pg_catalog"}
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return copyarchive.Receipt{}, err
		}
		defer func(cleanupCtx context.Context) { _ = conn.Close(context.WithoutCancel(cleanupCtx)) }(ctx)
		return copyarchive.Export(ctx, conn, d, tool, key, w, 4<<20)
	}
	return f, store, plan, oid, identity, artifact, b, produce, calls
}

func archiveWorkerLimits() state.ProjectEnvironmentClonePostgresArchiveLimits {
	return state.ProjectEnvironmentClonePostgresArchiveLimits{Count: 8, Bytes: 64 << 20}
}

func TestPGClonePostgresArchiveWorkerRecoversCommittedReceiptWithOriginalKeyAndNoSource(t *testing.T) {
	f, store, plan, oid, previous, artifact, b, produce, calls := cloneArchiveWorkerFixture(t)
	store.loseRecord = true
	b.losePut = true
	if _, err := f.srv.projectEnvironmentClonePostgresArchive(t.Context(), f.lease, plan, oid, artifact, 8<<20, archiveWorkerLimits(), produce); !errors.Is(err, managedpostgres.ErrUnavailable) || *calls != 1 || b.puts != 1 {
		t.Fatalf("committed upload/receipt reply loss: %v", err)
	}
	req, _ := plan.RequirementsForWorker()
	sourceID := req[0].Scope.SourceDatabaseID
	saved, err := store.ProjectEnvironmentClonePostgresArchiveForLease(t.Context(), f.lease, sourceID, oid)
	if err != nil || saved.State != "retained" {
		t.Fatalf("retained reply loss: %v", err)
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
	r, err := f.srv.projectEnvironmentClonePostgresArchive(t.Context(), f.lease, plan, oid, artifact, 1, archiveWorkerLimits(), nil)
	if err != nil || !copyarchive.SameReceipt(r, saved.Receipt) || *calls != 1 || b.puts != 1 {
		t.Fatalf("handoff redumped/replaced encrypted input: %v", err)
	}
	gets := b.gets
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current} }
	if _, err := f.srv.projectEnvironmentClonePostgresArchive(t.Context(), f.lease, plan, oid, artifact, 8<<20, archiveWorkerLimits(), produce); !errors.Is(err, managedpostgres.ErrUnavailable) || b.gets != gets || *calls != 1 {
		t.Fatalf("missing retained key recaptured source: %v", err)
	}
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{previous} }
	drift := artifact
	drift.Fingerprint = strings.Repeat("f", 64)
	if _, err := f.srv.projectEnvironmentClonePostgresArchive(t.Context(), f.lease, plan, oid, drift, 8<<20, archiveWorkerLimits(), produce); !errors.Is(err, managedpostgres.ErrConflict) || b.gets != gets {
		t.Fatalf("storage drift read another backend: %v", err)
	}
	stream, err := b.StorageBackend.Get(t.Context(), saved.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := io.ReadAll(stream)
	_ = stream.Close()
	if err != nil {
		t.Fatal(err)
	}
	cipher[len(cipher)-1] ^= 1
	if err := b.StorageBackend.Put(t.Context(), saved.StorageKey, bytes.NewReader(cipher)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.projectEnvironmentClonePostgresArchive(t.Context(), f.lease, plan, oid, artifact, 8<<20, archiveWorkerLimits(), produce); !errors.Is(err, managedpostgres.ErrConflict) || *calls != 1 || b.puts != 1 {
		t.Fatalf("corrupt retained archive recaptured: %v", err)
	}
	if f.lease.Operation.Resources[0].TargetID != "" || f.lease.Operation.Resources[0].Status != "captured" {
		t.Fatal("archive transfer supplied dataset readiness")
	}
}

func TestPGClonePostgresArchiveWorkerKeepsUncertainClaimsAndRecoversUndispatchedReservations(t *testing.T) {
	for _, fault := range []string{"reserve", "claim"} {
		t.Run(fault, func(t *testing.T) {
			f, store, plan, oid, _, artifact, b, produce, calls := cloneArchiveWorkerFixture(t)
			store.loseReserve = fault == "reserve"
			store.loseClaim = fault == "claim"
			if _, err := f.srv.projectEnvironmentClonePostgresArchive(t.Context(), f.lease, plan, oid, artifact, 8<<20, archiveWorkerLimits(), produce); !errors.Is(err, managedpostgres.ErrUnavailable) || *calls != 0 || b.puts != 0 {
				t.Fatalf("unknown intent dispatched producer: %v", err)
			}
			r, err := f.srv.projectEnvironmentClonePostgresArchive(t.Context(), f.lease, plan, oid, artifact, 8<<20, archiveWorkerLimits(), produce)
			if fault == "reserve" {
				if err != nil || r.CiphertextBytes == 0 || *calls != 1 || b.puts != 1 {
					t.Fatalf("undispatched intent did not resume: %v", err)
				}
			} else {
				if !errors.Is(err, managedpostgres.ErrUnavailable) || *calls != 0 || b.puts != 0 || b.gets != 1 {
					t.Fatalf("unknown claim repeated upload: %v", err)
				}
				var held int64
				if err := f.pool.QueryRow(t.Context(), "SELECT sum(reserved_bytes) FROM project_environment_clone_postgres_archives WHERE operation_id=$1", f.lease.Operation.ID).Scan(&held); err != nil || held != 8<<20 {
					t.Fatalf("uncertain owner released bytes: %d %v", held, err)
				}
			}
			if strings.Contains(fmt.Sprint(err), "private") {
				t.Fatal("storage diagnostics exposed")
			}
		})
	}
}

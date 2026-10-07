//go:build !no_pg

// adr: 590
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
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
	calls := new(int)
	produce := func(ctx context.Context, d copyinventory.DatabaseExport, key *age.X25519Recipient, w io.Writer) (copyarchive.Receipt, error) {
		(*calls)++
		if deadline, ok := ctx.Deadline(); !ok || deadline.After(f.lease.ExpiresAt) || key.String() != identity.Recipient().String() {
			return copyarchive.Receipt{}, managedpostgres.ErrConflict
		}
		return cloneWorkerArchiveFixture(ctx, d, key, w)
	}
	return f, store, plan, oid, identity, artifact, b, produce, calls
}

// This worker fixture supplies a synthetic encrypted archive in the private
// wire format. SQL inventory, receipt persistence, upload and verified recovery
// remain real. Export authentication and pg_dump are covered by copyarchive's
// own contracts; CI's plain TCP bootstrap is not a qualified export connection.
func cloneWorkerArchiveFixture(ctx context.Context, d copyinventory.DatabaseExport, key *age.X25519Recipient, output io.Writer) (copyarchive.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return copyarchive.Receipt{}, err
	}
	header, err := json.Marshal(struct {
		Version                                  int
		Scope                                    copyinventory.Scope
		InventoryFingerprint                     string
		Database                                 copyinventory.Database
		ReaderRoleOID                            uint32
		CapturedAllowConnections, ReaderDatabase bool
	}{1, d.Scope, d.InventoryFingerprint, d.Database, d.AuthenticatedReaderRoleOID, d.CapturedAllowConnections, d.AuthenticatedReaderDatabase})
	if err != nil {
		return copyarchive.Receipt{}, err
	}
	var cipher bytes.Buffer
	sealed, err := age.Encrypt(&cipher, key)
	if err != nil {
		return copyarchive.Receipt{}, err
	}
	dump := []byte("PGDMPfixturearchive")
	prefix := append([]byte("GRGPGD01"), binary.BigEndian.AppendUint32(nil, uint32(len(header)))...)
	if _, err := sealed.Write(append(append(prefix, header...), dump...)); err != nil {
		return copyarchive.Receipt{}, err
	}
	if err := sealed.Close(); err != nil {
		return copyarchive.Receipt{}, err
	}
	if _, err := output.Write(cipher.Bytes()); err != nil {
		return copyarchive.Receipt{}, err
	}
	digest := sha256.Sum256(cipher.Bytes())
	return copyarchive.Receipt{Scope: d.Scope, InventoryFingerprint: d.InventoryFingerprint, SourceDatabaseOID: d.Database.OID,
		PlainBytes: int64(len(dump)), CiphertextBytes: int64(cipher.Len()), CiphertextSHA256: hex.EncodeToString(digest[:])}, nil
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

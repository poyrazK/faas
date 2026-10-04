//go:build !no_pg

// adr:568
package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

type contentsWorkerFailureStore struct {
	*state.PgStore
	loseReserve, loseRecord bool
	hideCapturedOnce        bool
}

func (s *contentsWorkerFailureStore) ProjectEnvironmentClonePostgresContentsForLease(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, oid uint32) (state.ProjectEnvironmentClonePostgresContents, error) {
	a, err := s.PgStore.ProjectEnvironmentClonePostgresContentsForLease(ctx, l, id, oid)
	if err == nil && a.State == "captured" && s.hideCapturedOnce {
		s.hideCapturedOnce = false
		return state.ProjectEnvironmentClonePostgresContents{}, state.ErrNotFound
	}
	return a, err
}

func (s *contentsWorkerFailureStore) ReserveProjectEnvironmentClonePostgresContents(ctx context.Context, l state.ProjectEnvironmentCloneLease, r state.ProjectEnvironmentClonePostgresContentsRequest, limits state.ProjectEnvironmentClonePostgresContentsLimits) (state.ProjectEnvironmentClonePostgresContents, bool, error) {
	a, created, err := s.PgStore.ReserveProjectEnvironmentClonePostgresContents(ctx, l, r, limits)
	if err == nil && s.loseReserve {
		s.loseReserve = false
		return state.ProjectEnvironmentClonePostgresContents{}, false, managedpostgres.ErrUnavailable
	}
	return a, created, err
}
func (s *contentsWorkerFailureStore) RecordProjectEnvironmentClonePostgresContents(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, oid uint32, sealed copycontents.Sealed) (state.ProjectEnvironmentClonePostgresContents, error) {
	a, err := s.PgStore.RecordProjectEnvironmentClonePostgresContents(ctx, l, id, oid, sealed)
	if err == nil && s.loseRecord {
		s.loseRecord = false
		return state.ProjectEnvironmentClonePostgresContents{}, managedpostgres.ErrUnavailable
	}
	return a, err
}

type contentsWorkerFixture struct {
	f        cloneCoordinatorFixture
	store    *contentsWorkerFailureStore
	provider *cloneSnapshotProvider
	plan     copyinventory.ExportPlan
	d        copyinventory.DatabaseExport
	identity *age.X25519Identity
	read     clonePostgresContentsRead
	reads    *int
}

func newContentsWorkerFixture(t *testing.T) *contentsWorkerFixture {
	t.Helper()
	f, p, source, exports, oid, key, artifact, _, _ := cloneOwnedReaderArchiveFixture(t)
	store := &contentsWorkerFailureStore{PgStore: f.store.PgStore}
	f.srv.store = store
	x := &contentsWorkerFixture{f: f, store: store, provider: p, plan: exports, identity: key, reads: new(int)}
	inputs, err := exports.RequirementsForWorker()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range inputs {
		if d.Database.OID == oid {
			x.d = d
		}
	}
	if x.d.Database.OID == 0 {
		t.Fatal("selected source was omitted")
	}
	if _, _, err := store.ReserveProjectEnvironmentClonePostgresArchive(t.Context(), f.lease, state.ProjectEnvironmentClonePostgresArchiveRequest{
		Scope: x.d.Scope, DatabaseOID: oid, InventoryFingerprint: x.d.InventoryFingerprint, KeyID: key.Recipient().String(), StorageID: artifact.ID, StorageFingerprint: artifact.Fingerprint, ReservedBytes: 32 << 20}, archiveWorkerLimits()); err != nil {
		t.Fatal(err)
	}
	spool := t.TempDir()
	x.read = func(ctx context.Context, d copyinventory.DatabaseExport, k [32]byte) (copycontents.Manifest, error) {
		(*x.reads)++
		if deadline, ok := ctx.Deadline(); !ok || deadline.After(x.f.lease.ExpiresAt) {
			return copycontents.Manifest{}, managedpostgres.ErrConflict
		}
		owner, err := store.ProjectEnvironmentClonePostgresContentsForLease(ctx, x.f.lease, d.Scope.SourceDatabaseID, d.Database.OID)
		if err != nil || owner.State != "reserved" {
			return copycontents.Manifest{}, managedpostgres.ErrConflict
		}
		reader, err := store.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, x.f.lease, d.Scope.SourceDatabaseID)
		if err != nil {
			return copycontents.Manifest{}, err
		}
		snapshot, err := store.ProjectEnvironmentClonePostgresSnapshotForLease(ctx, x.f.lease, d.Scope.SourceDatabaseID)
		if err != nil {
			return copycontents.Manifest{}, err
		}
		capture, err := store.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, x.f.lease, d.Scope.SourceDatabaseID)
		if err != nil {
			return copycontents.Manifest{}, err
		}
		request := managedpostgres.SnapshotCopyReaderDatabaseSQLRequest{Reader: clonePostgresCopyReaderRequest(reader, snapshot, capture), Database: d}
		var manifest copycontents.Manifest
		err = x.f.srv.managedPostgres.WithSnapshotCopyReaderDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), request,
			func(ctx context.Context, c *pgx.Conn, _ managedpostgres.SnapshotCopyReaderSQLIdentity) error {
				var err error
				manifest, err = copycontents.Capture(ctx, c, d, copycontents.Config{Key: k, SpoolDir: spool, MaxBytes: 16 << 20, SortMemoryBytes: 64, SortDiskBytes: 1 << 20},
					func(ctx context.Context, conn *pgx.Conn, input copyinventory.DatabaseExport) error {
						if c != conn || !reflect.DeepEqual(input, d) {
							return managedpostgres.ErrConflict
						}
						current, err := store.ProjectEnvironmentClonePostgresContentsForLease(ctx, x.f.lease, d.Scope.SourceDatabaseID, d.Database.OID)
						if err != nil {
							return err
						}
						if current.OwnerID != owner.OwnerID || current.ReaderIdentitySHA256 != owner.ReaderIdentitySHA256 || current.ArchiveReservationSHA256 != owner.ArchiveReservationSHA256 {
							return managedpostgres.ErrConflict
						}
						return nil
					})
				return err
			})
		if err != nil {
			return copycontents.Manifest{}, err
		}
		return manifest, nil
	}
	return x
}

func contentsWorkerLimits() state.ProjectEnvironmentClonePostgresContentsLimits {
	return state.ProjectEnvironmentClonePostgresContentsLimits{Count: 4, Bytes: 4 * api.PostgresCopyCiphertextMaxBytes}
}
func (x *contentsWorkerFixture) capture(ctx context.Context, read clonePostgresContentsRead) (copycontents.Manifest, error) {
	return x.f.srv.projectEnvironmentClonePostgresContents(ctx, x.f.lease, x.plan, x.d.Database.OID, api.PostgresCopyCiphertextMaxBytes, contentsWorkerLimits(), read)
}
func (x *contentsWorkerFixture) owner(t *testing.T) state.ProjectEnvironmentClonePostgresContents {
	t.Helper()
	a, err := x.store.ProjectEnvironmentClonePostgresContentsForLease(t.Context(), x.f.lease, x.d.Scope.SourceDatabaseID, x.d.Database.OID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestPGClonePostgresContentsWorkerRecoversCommittedReplyWithOriginalKeyAndNoSource(t *testing.T) {
	x := newContentsWorkerFixture(t)
	x.store.loseRecord = true
	if _, err := x.capture(t.Context(), x.read); !errors.Is(err, managedpostgres.ErrUnavailable) || *x.reads != 1 {
		t.Fatal("committed reply loss", err)
	}
	first := x.owner(t)
	old := x.f.lease
	if err := x.store.ReleaseProjectEnvironmentCloneLease(t.Context(), x.f.lease, 0); err != nil {
		t.Fatal(err)
	}
	var err error
	x.f.lease, err = x.store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	rotated, _ := age.GenerateX25519Identity()
	setSecretRecipient = func() *age.X25519Recipient { return rotated.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{rotated, nil, x.identity} }
	x.f.srv.managedPostgres = nil
	manifest, err := x.capture(t.Context(), nil)
	if err != nil || manifest.Fingerprint() != first.Sealed.Fingerprint || *x.reads != 1 {
		t.Fatal("handoff recaptured or rebased source data", err)
	}
	recovered := x.owner(t)
	if recovered.OwnerID != first.OwnerID || recovered.KeyID != first.KeyID || !bytes.Equal(recovered.Sealed.Ciphertext, first.Sealed.Ciphertext) || !recovered.CapturedAt.Equal(first.CapturedAt) {
		t.Fatal("original manifest changed")
	}
	if _, err := x.store.ProjectEnvironmentClonePostgresContentsForLease(t.Context(), old, x.d.Scope.SourceDatabaseID, x.d.Database.OID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker read retained contents", err)
	}
	for _, r := range x.f.lease.Operation.Resources {
		if r.TargetID != "" || r.Status != "captured" {
			t.Fatal("manifest supplied stage readiness")
		}
	}
}

func TestPGClonePostgresContentsWorkerMissingOriginalKeyAndTamperingNeverRecapture(t *testing.T) {
	x := newContentsWorkerFixture(t)
	if _, err := x.capture(t.Context(), x.read); err != nil {
		t.Fatal(err)
	}
	rotated, _ := age.GenerateX25519Identity()
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{rotated} }
	if _, err := x.capture(t.Context(), x.read); !errors.Is(err, managedpostgres.ErrUnavailable) || *x.reads != 1 {
		t.Fatal("missing old key recaptured source", err)
	}
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{x.identity} }
	if _, err := x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_contents SET ciphertext=decode('00','hex') WHERE operation_id=$1", x.f.lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.capture(t.Context(), x.read); !errors.Is(err, state.ErrConflict) || *x.reads != 1 {
		t.Fatal("tampered owner recaptured source", err)
	}
}

func TestPGClonePostgresContentsWorkerReservationReplyLossRetainsOriginalRecipient(t *testing.T) {
	x := newContentsWorkerFixture(t)
	x.store.loseReserve = true
	if _, err := x.capture(t.Context(), x.read); !errors.Is(err, managedpostgres.ErrUnavailable) || *x.reads != 0 {
		t.Fatal("uncertain reservation dispatched source read", err)
	}
	first := x.owner(t)
	rotated, _ := age.GenerateX25519Identity()
	setSecretRecipient = func() *age.X25519Recipient { return rotated.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{rotated, x.identity} }
	if _, err := x.capture(t.Context(), x.read); err != nil || *x.reads != 1 {
		t.Fatal("original reservation did not resume", err)
	}
	recovered := x.owner(t)
	if recovered.OwnerID != first.OwnerID || recovered.KeyID != first.KeyID || recovered.State != "captured" {
		t.Fatal("original reserved recipient replaced")
	}
}

func TestPGClonePostgresContentsWorkerPostReadFailureKeepsChargedReservation(t *testing.T) {
	x := newContentsWorkerFixture(t)
	x.provider.readerSelectedSQLAfterError = managedpostgres.ErrUnavailable
	if _, err := x.capture(t.Context(), x.read); !errors.Is(err, managedpostgres.ErrUnavailable) || *x.reads != 1 || x.provider.readerSelectedSQLAfterCalls != 1 {
		t.Fatal("failed provider postcheck committed source proof", err)
	}
	first := x.owner(t)
	if first.State != "reserved" || !first.CapturedAt.IsZero() || len(first.Sealed.Ciphertext) != 0 {
		t.Fatal("failed read published contents")
	}
	x.provider.readerSelectedSQLAfterError = nil
	if _, err := x.capture(t.Context(), x.read); err != nil || *x.reads != 2 {
		t.Fatal("read-only capture retry failed", err)
	}
	if x.owner(t).OwnerID != first.OwnerID {
		t.Fatal("retry replaced charged owner")
	}
}

func TestPGClonePostgresContentsWorkerReservationSeesFirstCaptureAndNeverReReads(t *testing.T) {
	x := newContentsWorkerFixture(t)
	first, err := x.capture(t.Context(), x.read)
	if err != nil {
		t.Fatal(err)
	}
	// Model a concurrent capture committing after the first lookup and before
	// reservation: the reservation returns the first already captured owner.
	x.store.hideCapturedOnce = true
	recovered, err := x.capture(t.Context(), x.read)
	if err != nil || recovered.Fingerprint() != first.Fingerprint() || *x.reads != 1 {
		t.Fatal("existing first capture triggered a second source read", err)
	}
}

func TestPGClonePostgresContentsWorkerRejectsForeignManifestHandoffAndBudgetFailure(t *testing.T) {
	for _, mode := range []string{"foreign_manifest", "handoff", "cancel", "budget"} {
		t.Run(mode, func(t *testing.T) {
			x := newContentsWorkerFixture(t)
			read := func(ctx context.Context, d copyinventory.DatabaseExport, k [32]byte) (copycontents.Manifest, error) {
				if mode == "foreign_manifest" {
					d.Scope.OperationID = uuid.NewString()
				}
				// Independent real SQL capture with a fixture-only placement assertion
				// lets the worker exercise its complete original descriptor check.
				conn, err := x.provider.readerSelectedSQLConnect(ctx, d.Database.Name)
				if err != nil {
					return copycontents.Manifest{}, err
				}
				defer conn.Close(context.WithoutCancel(ctx))
				manifest, err := copycontents.Capture(ctx, conn, d, copycontents.Config{Key: k, SpoolDir: t.TempDir(), MaxBytes: 16 << 20}, func(context.Context, *pgx.Conn, copyinventory.DatabaseExport) error { return nil })
				if err != nil {
					return copycontents.Manifest{}, err
				}
				if mode == "handoff" {
					if err := x.store.ReleaseProjectEnvironmentCloneLease(ctx, x.f.lease, 0); err != nil {
						return copycontents.Manifest{}, err
					}
				}
				if mode == "cancel" {
					return copycontents.Manifest{}, context.Canceled
				}
				return manifest, nil
			}
			reserve := int64(api.PostgresCopyCiphertextMaxBytes)
			if mode == "budget" {
				reserve = 1
			}
			manifest, err := x.f.srv.projectEnvironmentClonePostgresContents(t.Context(), x.f.lease, x.plan, x.d.Database.OID, reserve, contentsWorkerLimits(), read)
			if err == nil || !reflect.DeepEqual(manifest, copycontents.Manifest{}) {
				t.Fatal("invalid result published original manifest", err)
			}
			if mode == "handoff" {
				x.f.lease, err = x.store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
				if err != nil {
					t.Fatal(err)
				}
			}
			if x.owner(t).State != "reserved" {
				t.Fatal("failed capture lost original reservation")
			}
		})
	}
}

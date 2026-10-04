//go:build !no_pg

// adr:566
package state_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func cloneContentsFixture(t *testing.T) (*state.PgStore, context.Context, *pgxpool.Pool, state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentClonePostgresContentsRequest, copycontents.Sealed) {
	t.Helper()
	s, ctx, pool, l, archive := clonePostgresArchiveFixture(t)
	id := archive.Scope.SourceDatabaseID
	r := claimCloneCopyReader(t, s, ctx, l, id)
	if _, err := s.RecordProjectEnvironmentClonePostgresCopyReader(ctx, l, id, copyReaderObservation(r)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, archive, archiveLimits()); err != nil {
		t.Fatal(err)
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	// The control-plane ledger owns opaque ciphertext. The separate contents
	// engine and APID worker tests qualify actual capture and authenticated open.
	cipher, err := secretbox.SealBytes(key.Recipient(), "private-ledger-fixture", []byte("private-stored-contents"), 0)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(cipher)
	sealed := copycontents.Sealed{Scope: archive.Scope, SourceDatabaseOID: archive.DatabaseOID, InventoryFingerprint: archive.InventoryFingerprint,
		KeyID: key.Recipient().String(), Fingerprint: strings.Repeat("e", 64), CiphertextSHA256: hex.EncodeToString(h[:]), Ciphertext: cipher}
	request := state.ProjectEnvironmentClonePostgresContentsRequest{Scope: archive.Scope, DatabaseOID: archive.DatabaseOID, InventoryFingerprint: archive.InventoryFingerprint, KeyID: sealed.KeyID, ReservedBytes: 4096}
	return s, ctx, pool, l, request, sealed
}

func contentsLimits() state.ProjectEnvironmentClonePostgresContentsLimits {
	return state.ProjectEnvironmentClonePostgresContentsLimits{Count: 3, Bytes: 16384}
}

func TestPgClonePostgresContentsSingleOwnerFirstCiphertextAndHandoff(t *testing.T) {
	s, ctx, pool, l, request, sealed := cloneContentsFixture(t)
	id := request.Scope.SourceDatabaseID
	type result struct {
		a       state.ProjectEnvironmentClonePostgresContents
		created bool
		err     error
	}
	ch := make(chan result, 6)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			a, created, err := s.ReserveProjectEnvironmentClonePostgresContents(ctx, l, request, contentsLimits())
			ch <- result{a, created, err}
		})
	}
	wg.Wait()
	close(ch)
	var original state.ProjectEnvironmentClonePostgresContents
	created := 0
	for got := range ch {
		if got.err != nil || got.a.State != "reserved" || !got.a.CapturedAt.IsZero() || len(got.a.Sealed.Ciphertext) != 0 {
			t.Fatal("initial reservation", got.err)
		}
		if original.OwnerID == "" {
			original = got.a
		}
		if got.a.OwnerID != original.OwnerID || !got.a.CreatedAt.Equal(original.CreatedAt) {
			t.Fatal("concurrent reservation changed original owner")
		}
		if got.created {
			created++
		}
	}
	if created != 1 || original.ArchiveOwnerID == "" || original.ReaderOwnerID == "" || original.InventoryCiphertextSHA256 == "" {
		t.Fatal("missing exact original lineage")
	}
	first, err := s.RecordProjectEnvironmentClonePostgresContents(ctx, l, id, request.DatabaseOID, sealed)
	if err != nil || first.State != "captured" || first.CapturedAt.IsZero() {
		t.Fatal("first capture", err)
	}
	sealed.Ciphertext[0] ^= 1 // returned/passed byte slices cannot mutate storage
	recovered, err := s.ProjectEnvironmentClonePostgresContentsForLease(ctx, l, id, request.DatabaseOID)
	if err != nil || bytes.Equal(recovered.Sealed.Ciphertext, sealed.Ciphertext) {
		t.Fatal("mutable ciphertext leaked into storage", err)
	}
	sealed.Ciphertext[0] ^= 1
	if replay, err := s.RecordProjectEnvironmentClonePostgresContents(ctx, l, id, request.DatabaseOID, sealed); err != nil || !replay.CapturedAt.Equal(first.CapturedAt) {
		t.Fatal("reply-loss replay", err)
	}
	changed := sealed
	changed.Ciphertext = bytes.Clone(sealed.Ciphertext)
	changed.Ciphertext[len(changed.Ciphertext)-1] ^= 1
	h := sha256.Sum256(changed.Ciphertext)
	changed.CiphertextSHA256 = hex.EncodeToString(h[:])
	if _, err := s.RecordProjectEnvironmentClonePostgresContents(ctx, l, id, request.DatabaseOID, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("replacement ciphertext accepted", err)
	}
	old := l
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, l, 0); err != nil {
		t.Fatal(err)
	}
	l, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresContentsForLease(ctx, old, id, request.DatabaseOID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale lease read", err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresContents(ctx, old, id, request.DatabaseOID, sealed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale lease wrote", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE managed_postgres_databases SET storage_limit_bytes=123456,restore_window_seconds=0 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	recovered, err = s.ProjectEnvironmentClonePostgresContentsForLease(ctx, l, id, request.DatabaseOID)
	if err != nil || recovered.OwnerID != first.OwnerID || !bytes.Equal(recovered.Sealed.Ciphertext, first.Sealed.Ciphertext) || !recovered.CapturedAt.Equal(first.CapturedAt) {
		t.Fatal("handoff rebased original contents", err)
	}
	op := l.Operation
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, op.Resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatal("contents metadata forged readiness", err)
	}
}

func TestPgClonePostgresContentsAccountCountBytesAndOriginalReservation(t *testing.T) {
	for _, dimension := range []string{"count", "bytes"} {
		t.Run(dimension, func(t *testing.T) {
			s, ctx, _, l, r, _ := cloneContentsFixture(t)
			limits := contentsLimits()
			if dimension == "count" {
				limits.Count = 1
			} else {
				limits.Bytes = r.ReservedBytes
			}
			if _, _, err := s.ReserveProjectEnvironmentClonePostgresContents(ctx, l, r, limits); err != nil {
				t.Fatal(err)
			}
			if _, created, err := s.ReserveProjectEnvironmentClonePostgresContents(ctx, l, r, limits); err != nil || created {
				t.Fatal("charged replay failed", err)
			}
			changed := r
			changed.ReservedBytes++
			if _, _, err := s.ReserveProjectEnvironmentClonePostgresContents(ctx, l, changed, limits); !errors.Is(err, state.ErrConflict) {
				t.Fatal("reservation bytes replaced", err)
			}
			key, _ := age.GenerateX25519Identity()
			changed = r
			changed.KeyID = key.Recipient().String()
			if _, _, err := s.ReserveProjectEnvironmentClonePostgresContents(ctx, l, changed, limits); !errors.Is(err, state.ErrConflict) {
				t.Fatal("reservation recipient replaced", err)
			}
			changed = r
			changed.DatabaseOID++
			archive := state.ProjectEnvironmentClonePostgresArchiveRequest{Scope: r.Scope, DatabaseOID: changed.DatabaseOID, InventoryFingerprint: r.InventoryFingerprint, KeyID: r.KeyID, StorageID: "private-artifacts", StorageFingerprint: strings.Repeat("d", 64), ReservedBytes: 8192}
			if _, _, err := s.ReserveProjectEnvironmentClonePostgresArchive(ctx, l, archive, archiveLimits()); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.ReserveProjectEnvironmentClonePostgresContents(ctx, l, changed, limits); !errors.Is(err, state.ErrQuotaExceeded) {
				t.Fatal("account reservation ceiling ignored", err)
			}
		})
	}
}

func TestPgClonePostgresContentsRejectsSourceAndParentSubstitution(t *testing.T) {
	s, ctx, pool, l, r, sealed := cloneContentsFixture(t)
	id := r.Scope.SourceDatabaseID
	if _, err := s.RecordProjectEnvironmentClonePostgresContents(ctx, l, id, r.DatabaseOID, sealed); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unowned capture accepted", err)
	}
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresContents(ctx, l, r, contentsLimits()); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"scope", "source_oid", "inventory", "recipient", "budget"} {
		t.Run(mode, func(t *testing.T) {
			input := sealed
			switch mode {
			case "scope":
				input.Scope.OperationID = uuid.NewString()
			case "source_oid":
				input.SourceDatabaseOID++
			case "inventory":
				input.InventoryFingerprint = strings.Repeat("f", 64)
			case "recipient":
				key, _ := age.GenerateX25519Identity()
				input.KeyID = key.Recipient().String()
			case "budget":
				input.Ciphertext = make([]byte, r.ReservedBytes+1)
				h := sha256.Sum256(input.Ciphertext)
				input.CiphertextSHA256 = hex.EncodeToString(h[:])
			}
			_, err := s.RecordProjectEnvironmentClonePostgresContents(ctx, l, id, r.DatabaseOID, input)
			if mode == "budget" {
				if !errors.Is(err, state.ErrQuotaExceeded) {
					t.Fatal(err)
				}
			} else if !errors.Is(err, state.ErrConflict) {
				t.Fatal("substituted manifest accepted", err)
			}
		})
	}
	oversized := sealed
	oversized.Ciphertext = make([]byte, api.PostgresCopyCiphertextMaxBytes+1)
	if _, err := s.RecordProjectEnvironmentClonePostgresContents(ctx, l, id, r.DatabaseOID, oversized); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatal("global envelope limit ignored", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE project_environment_clone_postgres_copy_readers SET available=false WHERE operation_id=$1", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresContents(ctx, l, id, r.DatabaseOID, sealed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unavailable reader published capture", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE project_environment_clone_postgres_copy_readers SET available=true WHERE operation_id=$1", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresContents(ctx, l, id, r.DatabaseOID, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE project_environment_clone_postgres_archives SET storage_fingerprint=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresContentsForLease(ctx, l, id, r.DatabaseOID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("archive reservation replacement adopted original capture", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE project_environment_clone_postgres_archives SET storage_fingerprint=$2 WHERE operation_id=$1", l.Operation.ID, strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE project_environment_clone_postgres_copy_readers SET endpoint_id='substituted-reader' WHERE operation_id=$1", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentClonePostgresContentsForLease(ctx, l, id, r.DatabaseOID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("reader identity replacement adopted original capture", err)
	}
}

func TestPgClonePostgresContentsSurvivesReaderCleanupAndGuardsDowngrade(t *testing.T) {
	s, ctx, pool, l, r, sealed := cloneContentsFixture(t)
	id := r.Scope.SourceDatabaseID
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresContents(ctx, l, r, contentsLimits()); err != nil {
		t.Fatal(err)
	}
	first, err := s.RecordProjectEnvironmentClonePostgresContents(ctx, l, id, r.DatabaseOID, sealed)
	if err != nil {
		t.Fatal(err)
	}
	l = cloneForkCompensation(t, s, ctx, l)
	reader, err := s.BeginProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id)
	if err != nil {
		t.Fatal(err)
	}
	reader, dispatch, err := s.ClaimProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id)
	if err != nil || !dispatch {
		t.Fatal(err)
	}
	proof := state.ProjectEnvironmentClonePostgresCopyReaderDeletion{EndpointID: reader.EndpointID, CreatedAt: reader.EndpointCreatedAt, OperationIDs: []string{"original-reader-delete"}, Done: true}
	if _, err := s.RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(ctx, l, id, proof); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, l, id, proof); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.ProjectEnvironmentClonePostgresContentsForLease(ctx, l, id, r.DatabaseOID)
	if err != nil || recovered.OwnerID != first.OwnerID || !bytes.Equal(recovered.Sealed.Ciphertext, first.Sealed.Ciphertext) {
		t.Fatal("authenticated input cleanup discarded contents", err)
	}
	if _, err := s.RecordProjectEnvironmentClonePostgresContents(ctx, l, id, r.DatabaseOID, sealed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("compensation captured/replaced manifest", err)
	}
	var holds, heldBytes int64
	if err := pool.QueryRow(ctx, "SELECT count(*),sum(reserved_bytes)::bigint FROM project_environment_clone_postgres_contents WHERE account_id=$1", l.Operation.AccountID).Scan(&holds, &heldBytes); err != nil || holds != 1 || heldBytes != r.ReservedBytes {
		t.Fatal("reader cleanup released manifest quota", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err := tx.Exec(ctx, "ALTER TABLE project_environment_clone_postgres_contents ADD CONSTRAINT postgres_contents_down_no_ownership CHECK(false)"); err == nil {
		t.Fatal("downgrade discarded original contents ownership")
	}
}

func TestPgClonePostgresContentsRechecksExpiredLeaseAfterReceiptLock(t *testing.T) {
	s, ctx, pool, l, r, _ := cloneContentsFixture(t)
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresContents(ctx, l, r, contentsLimits()); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "UPDATE project_environment_clone_operations SET lease_until=clock_timestamp()+interval '300 milliseconds' WHERE id=$1 RETURNING lease_until", l.Operation.ID).Scan(&l.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err := tx.Exec(ctx, "SELECT operation_id FROM project_environment_clone_postgres_contents WHERE operation_id=$1 FOR UPDATE", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.ProjectEnvironmentClonePostgresContentsForLease(ctx, l, r.Scope.SourceDatabaseID, r.DatabaseOID)
		done <- err
	}()
	time.Sleep(500 * time.Millisecond)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired waiter read private contents", err)
	}
}

func TestPgClonePostgresContentsMigrationRoundTripAndOwnedDownRefusal(t *testing.T) {
	raw, err := migrations.FS.ReadFile("20261004095153292_environment_clone_postgres_contents.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("contents migration lacks downgrade")
	}
	up, down := strings.TrimPrefix(parts[0], "-- +goose Up"), parts[1]
	childUp, childDown := cloneVerificationMigrationParts(t)
	_, ctx, pool := pgWithPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	shape := `SELECT (SELECT jsonb_agg(jsonb_build_array(attname,format_type(atttypid,atttypmod),attnotnull) ORDER BY attnum)::text FROM pg_attribute WHERE attrelid='project_environment_clone_postgres_contents'::regclass AND attnum>0 AND NOT attisdropped),(SELECT jsonb_agg(jsonb_build_array(conrelid::regclass::text,conname,pg_get_constraintdef(oid)) ORDER BY conrelid::regclass::text,conname)::text FROM pg_constraint WHERE conrelid IN ('project_environment_clone_postgres_contents'::regclass,'project_environment_clone_postgres_archives'::regclass,'project_environment_clone_postgres_inventories'::regclass,'project_environment_clone_postgres_copy_readers'::regclass))`
	var a, b, c, d string
	if err := tx.QueryRow(ctx, shape).Scan(&a, &b); err != nil {
		t.Fatal(err)
	}
	// Reverse the dependent child first, as an actual ordered downgrade would.
	if _, err := tx.Exec(ctx, childDown); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, childUp); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, shape).Scan(&c, &d); err != nil || a != c || b != d {
		t.Fatal("migration round trip changed original lineage shape", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	s, ctx, pool, l, r, sealed := cloneContentsFixture(t)
	if _, _, err := s.ReserveProjectEnvironmentClonePostgresContents(ctx, l, r, contentsLimits()); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"reserved", "captured"} {
		if phase == "captured" {
			if _, err := s.RecordProjectEnvironmentClonePostgresContents(ctx, l, r.Scope.SourceDatabaseID, r.DatabaseOID, sealed); err != nil {
				t.Fatal(err)
			}
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, downErr := tx.Exec(ctx, down)
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if downErr == nil {
			t.Fatal("owned contents allowed downgrade", phase)
		}
		owner, err := s.ProjectEnvironmentClonePostgresContentsForLease(ctx, l, r.Scope.SourceDatabaseID, r.DatabaseOID)
		if err != nil || owner.State != phase {
			t.Fatal("refused downgrade erased original owner", err)
		}
	}
}

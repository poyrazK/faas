//go:build !no_pg

// adr: 583
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/state"
)

func cloneVerificationFixture(t *testing.T) (databaseSQLPinsFixture, state.ProjectEnvironmentClonePostgresVerificationRequest, copycontents.SealedMatch) {
	t.Helper()
	x, _, imported := cloneBoundImportFixture(t)
	f := x.f
	id := imported.Input.Scope.SourceDatabaseID
	if _, _, err := f.s.ReserveProjectEnvironmentClonePostgresImport(f.ctx, f.l, imported); err != nil {
		t.Fatal(err)
	}
	owner, _, err := f.s.ClaimProjectEnvironmentClonePostgresImport(f.ctx, f.l, id, x.oid)
	if err != nil {
		t.Fatal(err)
	}
	r := claimCloneCopyReader(t, f.s, f.ctx, f.l, id)
	if _, err := f.s.RecordProjectEnvironmentClonePostgresCopyReader(f.ctx, f.l, id, copyReaderObservation(r)); err != nil {
		t.Fatal(err)
	}
	// These are explicitly opaque ledger fixtures. Actual data MAC authentication
	// and native closure are exercised by the core and APID integration contracts.
	cipher := []byte("opaque original contents ledger fixture")
	h := sha256.Sum256(cipher)
	contents := copycontents.Sealed{Scope: imported.Input.Scope, SourceDatabaseOID: x.oid, InventoryFingerprint: imported.Input.InventoryFingerprint,
		Fingerprint: strings.Repeat("e", 64), KeyID: f.key.Recipient().String(), Ciphertext: cipher, CiphertextSHA256: hex.EncodeToString(h[:])}
	if _, _, err := f.s.ReserveProjectEnvironmentClonePostgresContents(f.ctx, f.l, state.ProjectEnvironmentClonePostgresContentsRequest{
		Scope: contents.Scope, DatabaseOID: x.oid, InventoryFingerprint: contents.InventoryFingerprint, KeyID: contents.KeyID, ReservedBytes: 4096}, contentsLimits()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.RecordProjectEnvironmentClonePostgresContents(f.ctx, f.l, id, x.oid, contents); err != nil {
		t.Fatal(err)
	}
	request := state.ProjectEnvironmentClonePostgresVerificationRequest{Scope: contents.Scope, DatabaseOID: x.oid, ContentsCiphertextSHA256: contents.CiphertextSHA256,
		DatabaseSQLPinsCiphertextSHA256: imported.DatabaseSQLPinsCiphertextSHA256, ImportID: owner.ImportID, TargetFingerprint: owner.TargetFingerprint, KeyID: contents.KeyID, ReservedBytes: 4096}
	cipher = []byte("opaque independent comparison ledger fixture")
	h = sha256.Sum256(cipher)
	proof := copycontents.SealedMatch{Scope: contents.Scope, SourceDatabaseOID: x.oid, TargetDatabaseOID: imported.Target.DatabaseOID, ImportID: owner.ImportID,
		OpenedAt: time.Now().UTC().Truncate(time.Microsecond), ManifestFingerprint: contents.Fingerprint, TargetFingerprint: owner.TargetFingerprint, Fingerprint: strings.Repeat("d", 64),
		KeyID: contents.KeyID, Ciphertext: cipher, CiphertextSHA256: hex.EncodeToString(h[:])}
	return x, request, proof
}

func TestPgClonePostgresVerificationConcurrentOwnerClaimAndFirstCiphertext(t *testing.T) {
	x, request, proof := cloneVerificationFixture(t)
	f := x.f
	type result struct {
		v     state.ProjectEnvironmentClonePostgresVerification
		first bool
		err   error
	}
	var wg sync.WaitGroup
	results := make(chan result, 6)
	for range 6 {
		wg.Go(func() {
			v, first, err := f.s.ReserveProjectEnvironmentClonePostgresVerification(f.ctx, f.l, request)
			results <- result{v, first, err}
		})
	}
	wg.Wait()
	close(results)
	var original state.ProjectEnvironmentClonePostgresVerification
	firsts := 0
	for r := range results {
		if r.err != nil || r.v.State != "reserved" {
			t.Fatal("reservation", r.err)
		}
		if original.VerificationID == "" {
			original = r.v
		}
		if r.v.VerificationID != original.VerificationID || !r.v.CreatedAt.Equal(original.CreatedAt) {
			t.Fatal("concurrent owner changed")
		}
		if r.first {
			firsts++
		}
	}
	if firsts != 1 {
		t.Fatal("multiple original reservations")
	}
	proof.OwnerID = original.VerificationID
	if _, err := f.s.RecordProjectEnvironmentClonePostgresVerificationMatch(f.ctx, f.l, request.Scope.SourceDatabaseID, x.oid, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatal("undispatched comparison accepted", err)
	}
	results = make(chan result, 6)
	for range 6 {
		wg.Go(func() {
			v, first, err := f.s.ClaimProjectEnvironmentClonePostgresVerification(f.ctx, f.l, request.Scope.SourceDatabaseID, x.oid)
			results <- result{v, first, err}
		})
	}
	wg.Wait()
	close(results)
	firsts = 0
	for r := range results {
		if r.err != nil || r.v.State != "verifying" || r.v.RequestStartedAt.IsZero() {
			t.Fatal("claim", r.err)
		}
		if r.first {
			firsts++
		}
	}
	if firsts != 1 {
		t.Fatal("multiple first claims")
	}
	compared, err := f.s.RecordProjectEnvironmentClonePostgresVerificationMatch(f.ctx, f.l, request.Scope.SourceDatabaseID, x.oid, proof)
	if err != nil || compared.State != "compared" {
		t.Fatal("first opaque ciphertext", err)
	}
	proof.Ciphertext[0] ^= 1
	recovered, err := f.s.ProjectEnvironmentClonePostgresVerificationForLease(f.ctx, f.l, request.Scope.SourceDatabaseID, x.oid)
	if err != nil || bytes.Equal(recovered.Sealed.Ciphertext, proof.Ciphertext) {
		t.Fatal("mutable input escaped", err)
	}
	proof.Ciphertext[0] ^= 1
	if replay, err := f.s.RecordProjectEnvironmentClonePostgresVerificationMatch(f.ctx, f.l, request.Scope.SourceDatabaseID, x.oid, proof); err != nil || !replay.ComparedAt.Equal(compared.ComparedAt) {
		t.Fatal("reply replay", err)
	}
	changed := proof
	changed.Ciphertext = bytes.Clone(proof.Ciphertext)
	changed.Ciphertext[0] ^= 1
	h := sha256.Sum256(changed.Ciphertext)
	changed.CiphertextSHA256 = hex.EncodeToString(h[:])
	if _, err := f.s.RecordProjectEnvironmentClonePostgresVerificationMatch(f.ctx, f.l, request.Scope.SourceDatabaseID, x.oid, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("first ciphertext replaced", err)
	}
	if _, err := f.s.RecordProjectEnvironmentClonePostgresVerificationClosure(f.ctx, f.l, request.Scope.SourceDatabaseID, x.oid, state.ProjectEnvironmentClonePostgresVerificationCompletion{}); !errors.Is(err, state.ErrConflict) {
		t.Fatal("metadata forged actual data/closure", err)
	}
	if _, dispatch, err := f.s.ClaimProjectEnvironmentClonePostgresVerification(f.ctx, f.l, request.Scope.SourceDatabaseID, x.oid); err != nil || dispatch {
		t.Fatal("compared owner redispatched", err)
	}
	op := f.l.Operation
	if _, err := f.s.AdvanceProjectEnvironmentCloneOperation(f.ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, op.Resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatal("comparison supplied stage readiness", err)
	}
}

func TestPgClonePostgresVerificationOriginalPinsBudgetAndHandoff(t *testing.T) {
	x, request, proof := cloneVerificationFixture(t)
	f := x.f
	id := request.Scope.SourceDatabaseID
	original, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerification(f.ctx, f.l, request)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"contents", "pins", "import", "target", "recipient", "bytes", "scope"} {
		t.Run(mode, func(t *testing.T) {
			r := request
			switch mode {
			case "contents":
				r.ContentsCiphertextSHA256 = strings.Repeat("c", 64)
			case "pins":
				r.DatabaseSQLPinsCiphertextSHA256 = strings.Repeat("c", 64)
			case "import":
				r.ImportID = uuid.NewString()
			case "target":
				r.TargetFingerprint = strings.Repeat("c", 64)
			case "recipient":
				r.KeyID = otherKey.Recipient().String()
			case "bytes":
				r.ReservedBytes++
			case "scope":
				r.Scope.SourceVersion = strings.Repeat("c", 64)
			}
			if _, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerification(f.ctx, f.l, r); !errors.Is(err, state.ErrConflict) {
				t.Fatal("occupied identity/budget replaced", err)
			}
		})
	}
	tooLarge := request
	tooLarge.ReservedBytes = api.PostgresCopyVerificationCiphertextMaxBytes + 1
	if _, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerification(f.ctx, f.l, tooLarge); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("structural result cap", err)
	}
	if _, _, err := f.s.ClaimProjectEnvironmentClonePostgresVerification(f.ctx, f.l, id, x.oid); err != nil {
		t.Fatal(err)
	}
	old := f.l
	if err := f.s.ReleaseProjectEnvironmentCloneLease(f.ctx, old, 0); err != nil {
		t.Fatal(err)
	}
	f.l, err = f.s.ClaimNextProjectEnvironmentClone(f.ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ProjectEnvironmentClonePostgresVerificationForLease(f.ctx, old, id, x.oid); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale read", err)
	}
	proof.OwnerID = original.VerificationID
	if _, err := f.s.RecordProjectEnvironmentClonePostgresVerificationMatch(f.ctx, old, id, x.oid, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale match publication", err)
	}
	v, dispatch, err := f.s.ClaimProjectEnvironmentClonePostgresVerification(f.ctx, f.l, id, x.oid)
	if err != nil || dispatch || v.VerificationID != original.VerificationID || v.ReservedBytes != request.ReservedBytes {
		t.Fatal("handoff lost original claim/budget", err)
	}
	if _, err := f.s.RecordProjectEnvironmentClonePostgresVerificationMatch(f.ctx, f.l, id, x.oid, proof); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE project_environment_clone_postgres_contents SET fingerprint=$2 WHERE operation_id=$1", f.l.Operation.ID, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ProjectEnvironmentClonePostgresVerificationForLease(f.ctx, f.l, id, x.oid); !errors.Is(err, state.ErrConflict) {
		t.Fatal("substituted original manifest recovered", err)
	}
}

func TestPgClonePostgresVerificationExpiryAfterProofLock(t *testing.T) {
	x, r, _ := cloneVerificationFixture(t)
	f := x.f
	if _, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerification(f.ctx, f.l, r); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, "UPDATE project_environment_clone_operations SET lease_until=clock_timestamp()+interval '300 milliseconds' WHERE id=$1 RETURNING lease_until", f.l.Operation.ID).Scan(&f.l.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(f.ctx, "SELECT operation_id FROM project_environment_clone_postgres_verifications WHERE operation_id=$1 FOR UPDATE", f.l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.s.ProjectEnvironmentClonePostgresVerificationForLease(f.ctx, f.l, r.Scope.SourceDatabaseID, r.DatabaseOID)
		done <- err
	}()
	time.Sleep(500 * time.Millisecond)
	if err := tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired lease acquired proof after wait", err)
	}
}

func cloneVerificationMigrationParts(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261004095153296_environment_clone_postgres_verifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("missing Down")
	}
	attemptUp, attemptDown := cloneVerificationAttemptMigrationParts(t)
	return strings.TrimPrefix(parts[0], "-- +goose Up") + "\n" + attemptUp, attemptDown + "\n" + parts[1]
}

func TestPgClonePostgresVerificationMigrationRoundTripAndOwnedDownRefusal(t *testing.T) {
	up, down := cloneVerificationMigrationParts(t)
	_, ctx, pool := pgWithPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	shape := `SELECT jsonb_build_array(
 (SELECT jsonb_agg(jsonb_build_array(attname,format_type(atttypid,atttypmod),attnotnull) ORDER BY attnum) FROM pg_attribute WHERE attrelid='project_environment_clone_postgres_verifications'::regclass AND attnum>0 AND NOT attisdropped),
 (SELECT jsonb_agg(jsonb_build_array(conrelid::regclass::text,conname,pg_get_constraintdef(oid)) ORDER BY conrelid::regclass::text,conname) FROM pg_constraint WHERE conrelid IN ('project_environment_clone_postgres_verifications'::regclass,'project_environment_clone_postgres_contents'::regclass,'project_environment_clone_postgres_imports'::regclass,'project_environment_clone_postgres_database_sql_pins'::regclass)))::text`
	var before, after string
	if err := tx.QueryRow(ctx, shape).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, shape).Scan(&after); err != nil || before != after {
		t.Fatal("round trip changed columns or original lineage constraints", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	x, r, proof := cloneVerificationFixture(t)
	f := x.f
	v, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerification(f.ctx, f.l, r)
	if err != nil {
		t.Fatal(err)
	}
	proof.OwnerID = v.VerificationID
	for _, phase := range []string{"reserved", "verifying", "compared"} {
		if phase == "verifying" {
			_, _, err = f.s.ClaimProjectEnvironmentClonePostgresVerification(f.ctx, f.l, r.Scope.SourceDatabaseID, r.DatabaseOID)
		}
		if phase == "compared" {
			_, err = f.s.RecordProjectEnvironmentClonePostgresVerificationMatch(f.ctx, f.l, r.Scope.SourceDatabaseID, r.DatabaseOID, proof)
		}
		if err != nil {
			t.Fatal(err)
		}
		tx, err := f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, downErr := tx.Exec(f.ctx, down)
		_ = tx.Rollback(f.ctx)
		var pgErr *pgconn.PgError
		if !errors.As(downErr, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "postgres_verifications_down_no_ownership" {
			t.Fatal("owned phase did not refuse downgrade at its ownership guard", phase, downErr)
		}
		got, err := f.s.ProjectEnvironmentClonePostgresVerificationForLease(f.ctx, f.l, r.Scope.SourceDatabaseID, r.DatabaseOID)
		if err != nil || got.State != phase || got.VerificationID != v.VerificationID {
			t.Fatal("refused downgrade lost owner", err)
		}
	}
}

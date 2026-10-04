//go:build !no_pg

// adr:531
package state_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
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

func cloneVerificationAttemptMigrationParts(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261004095153299_environment_clone_postgres_verification_attempts.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("missing Down")
	}
	readUp, readDown := cloneVerificationReadBudgetMigrationParts(t)
	return parts[0] + "\n" + readUp, readDown + "\n" + parts[1]
}

// An explicitly opaque SQL ledger fixture, never a native closure capability.
// Actual closure admission and successful native proof composition are tested
// with restored data in APID, independently of these ownership contracts.
func cloneFailedVerificationFixture(t *testing.T) (databaseSQLPinsFixture, state.ProjectEnvironmentClonePostgresVerificationRetryRequest, copycontents.SealedMatch) {
	t.Helper()
	x, r, proof := cloneVerificationFixture(t)
	f := x.f
	id := r.Scope.SourceDatabaseID
	original, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerification(f.ctx, f.l, r)
	if err != nil {
		t.Fatal(err)
	}
	original, _, err = f.s.ClaimProjectEnvironmentClonePostgresVerification(f.ctx, f.l, id, x.oid)
	if err != nil {
		t.Fatal(err)
	}
	proof.OpenedAt = time.Now().UTC().Truncate(time.Microsecond)
	_, err = f.pool.Exec(f.ctx, `INSERT INTO project_environment_clone_postgres_verification_attempts
 (operation_id,source_database_id,database_oid,original_verification_id,attempt,verification_id,key_id,reserved_bytes,state,created_at,request_started_at,window_opened_at,target_database_oid,native_closed_at,failed_at)
 VALUES($1,$2,$3,$4,1,$4,$5,$6,'failed',$7,$8,$9,$10,$9,clock_timestamp())`, original.Scope.OperationID, id, x.oid, original.VerificationID, original.KeyID, original.ReservedBytes, original.CreatedAt, original.RequestStartedAt, proof.OpenedAt, proof.TargetDatabaseOID)
	if err != nil {
		t.Fatal(err)
	}
	return x, state.ProjectEnvironmentClonePostgresVerificationRetryRequest{ProjectEnvironmentClonePostgresVerificationRequest: r, PreviousVerificationID: original.VerificationID}, proof
}
func failVerificationLedgerAttempt(t *testing.T, x databaseSQLPinsFixture, a state.ProjectEnvironmentClonePostgresVerificationAttempt, targetOID uint32) {
	t.Helper()
	_, err := x.f.pool.Exec(x.f.ctx, `UPDATE project_environment_clone_postgres_verification_attempts SET state='failed',window_opened_at=$2,target_database_oid=$3,native_closed_at=$2,failed_at=clock_timestamp() WHERE verification_id=$1`, a.VerificationID, time.Now().UTC().Truncate(time.Microsecond), targetOID)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPgClonePostgresVerificationAttemptsRequireActualFailureAndKeepFirstOwner(t *testing.T) {
	x, r, proof := cloneVerificationFixture(t)
	f := x.f
	id := r.Scope.SourceDatabaseID
	original, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerification(f.ctx, f.l, r)
	if err != nil {
		t.Fatal(err)
	}
	request := state.ProjectEnvironmentClonePostgresVerificationRetryRequest{ProjectEnvironmentClonePostgresVerificationRequest: r, PreviousVerificationID: original.VerificationID}
	for _, phase := range []string{"reserved", "verifying", "compared"} {
		if phase == "verifying" {
			_, _, err = f.s.ClaimProjectEnvironmentClonePostgresVerification(f.ctx, f.l, id, x.oid)
		}
		if phase == "compared" {
			proof.OwnerID = original.VerificationID
			_, err = f.s.RecordProjectEnvironmentClonePostgresVerificationMatch(f.ctx, f.l, id, x.oid, proof)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.RecordProjectEnvironmentClonePostgresVerificationFailure(f.ctx, f.l, id, x.oid, original.VerificationID, state.ProjectEnvironmentClonePostgresVerificationFailure{}); !errors.Is(err, state.ErrConflict) {
			t.Fatal("zero closure accepted", phase, err)
		}
		if _, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerificationRetry(f.ctx, f.l, request); !errors.Is(err, state.ErrConflict) {
			t.Fatal("unclosed/compared owner authorized retry", phase, err)
		}
		if history, err := f.s.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(f.ctx, f.l, id, x.oid); err != nil || len(history) != 0 {
			t.Fatal("failed command left history", err)
		}
	}
}

func TestPgClonePostgresVerificationAttemptsConcurrentAdmissionAndRetainedCiphertext(t *testing.T) {
	x, r, proof := cloneFailedVerificationFixture(t)
	f := x.f
	id := r.Scope.SourceDatabaseID
	type result struct {
		a     state.ProjectEnvironmentClonePostgresVerificationAttempt
		first bool
		err   error
	}
	run := func(claim bool) state.ProjectEnvironmentClonePostgresVerificationAttempt {
		results := make(chan result, 6)
		var wg sync.WaitGroup
		for range 6 {
			wg.Go(func() {
				var a state.ProjectEnvironmentClonePostgresVerificationAttempt
				var first bool
				var err error
				if claim {
					history, e := f.s.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(f.ctx, f.l, id, x.oid)
					if e != nil {
						results <- result{err: e}
						return
					}
					a, first, err = f.s.ClaimProjectEnvironmentClonePostgresVerificationAttempt(f.ctx, f.l, id, x.oid, history[1].VerificationID)
				} else {
					a, first, err = f.s.ReserveProjectEnvironmentClonePostgresVerificationRetry(f.ctx, f.l, r)
				}
				results <- result{a, first, err}
			})
		}
		wg.Wait()
		close(results)
		var original state.ProjectEnvironmentClonePostgresVerificationAttempt
		firsts := 0
		for got := range results {
			if got.err != nil {
				t.Fatal(got.err)
			}
			if original.VerificationID == "" {
				original = got.a
			}
			if !reflect.DeepEqual(got.a, original) {
				t.Fatal("concurrent intent changed owner")
			}
			if got.first {
				firsts++
			}
		}
		if firsts != 1 {
			t.Fatal("not one admission", firsts)
		}
		return original
	}
	reserved := run(false)
	a := run(true)
	if a.Attempt != 2 || a.PreviousVerificationID != r.PreviousVerificationID || a.State != "verifying" || a.VerificationID == r.PreviousVerificationID || !a.CreatedAt.Equal(reserved.CreatedAt) {
		t.Fatal("retry changed predecessor or claim identity")
	}
	if _, _, err := f.s.ClaimProjectEnvironmentClonePostgresVerification(f.ctx, f.l, id, x.oid); !errors.Is(err, state.ErrConflict) {
		t.Fatal("original failed owner reclaimed", err)
	}
	proof.OwnerID = r.PreviousVerificationID
	if _, err := f.s.RecordProjectEnvironmentClonePostgresVerificationMatch(f.ctx, f.l, id, x.oid, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatal("original owner compared after failure", err)
	}
	proof.OwnerID = a.VerificationID
	proof.OpenedAt = time.Now().UTC().Truncate(time.Microsecond)
	compared, err := f.s.RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(f.ctx, f.l, id, x.oid, a.VerificationID, proof)
	if err != nil || compared.State != "compared" {
		t.Fatal("retry retained match", err)
	}
	proof.Ciphertext[0] ^= 1
	history, err := f.s.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(f.ctx, f.l, id, x.oid)
	if err != nil || len(history) != 2 || bytes.Equal(history[1].Sealed.Ciphertext, proof.Ciphertext) {
		t.Fatal("caller mutated retained evidence", err)
	}
	proof.Ciphertext[0] ^= 1
	replay, err := f.s.RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(f.ctx, f.l, id, x.oid, a.VerificationID, proof)
	if err != nil || !reflect.DeepEqual(replay, compared) {
		t.Fatal("lost response changed first proof", err)
	}
	changed := proof
	changed.Ciphertext = bytes.Clone(proof.Ciphertext)
	changed.Ciphertext[0] ^= 1
	h := sha256.Sum256(changed.Ciphertext)
	changed.CiphertextSHA256 = hex.EncodeToString(h[:])
	if _, err := f.s.RecordProjectEnvironmentClonePostgresVerificationAttemptMatch(f.ctx, f.l, id, x.oid, a.VerificationID, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("first proof replaced", err)
	}
	if _, err := f.s.RecordProjectEnvironmentClonePostgresVerificationAttemptClosure(f.ctx, f.l, id, x.oid, a.VerificationID, state.ProjectEnvironmentClonePostgresVerificationCompletion{}); !errors.Is(err, state.ErrConflict) {
		t.Fatal("opaque metadata manufactured closure", err)
	}
	if _, err := f.s.RecordProjectEnvironmentClonePostgresVerificationFailure(f.ctx, f.l, id, x.oid, a.VerificationID, state.ProjectEnvironmentClonePostgresVerificationFailure{}); !errors.Is(err, state.ErrConflict) {
		t.Fatal("compared attempt recorded failed", err)
	}
	r.PreviousVerificationID = a.VerificationID
	if _, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerificationRetry(f.ctx, f.l, r); !errors.Is(err, state.ErrConflict) {
		t.Fatal("compared attempt granted retry", err)
	}
}

func TestPgClonePostgresVerificationAttemptsParentsRecipientBudgetAndCeiling(t *testing.T) {
	x, r, proof := cloneFailedVerificationFixture(t)
	f := x.f
	id := r.Scope.SourceDatabaseID
	a, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerificationRetry(f.ctx, f.l, r)
	if err != nil {
		t.Fatal(err)
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"predecessor", "contents", "pins", "scope", "import", "target", "recipient", "budget"} {
		t.Run(mode, func(t *testing.T) {
			changed := r
			switch mode {
			case "predecessor":
				changed.PreviousVerificationID = uuid.NewString()
			case "contents":
				changed.ContentsCiphertextSHA256 = strings.Repeat("c", 64)
			case "pins":
				changed.DatabaseSQLPinsCiphertextSHA256 = strings.Repeat("c", 64)
			case "scope":
				changed.Scope.SourceVersion = strings.Repeat("c", 64)
			case "import":
				changed.ImportID = uuid.NewString()
			case "target":
				changed.TargetFingerprint = strings.Repeat("c", 64)
			case "recipient":
				changed.KeyID = key.Recipient().String()
			case "budget":
				changed.ReservedBytes++
			}
			if _, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerificationRetry(f.ctx, f.l, changed); !errors.Is(err, state.ErrConflict) {
				t.Fatal("occupied reservation changed", mode, err)
			}
		})
	}
	for attempt := int32(2); attempt <= api.PostgresCopyVerificationAttemptsMax; attempt++ {
		a, _, err = f.s.ClaimProjectEnvironmentClonePostgresVerificationAttempt(f.ctx, f.l, id, x.oid, a.VerificationID)
		if err != nil {
			t.Fatal(err)
		}
		failVerificationLedgerAttempt(t, x, a, proof.TargetDatabaseOID)
		r.PreviousVerificationID = a.VerificationID
		next, first, err := f.s.ReserveProjectEnvironmentClonePostgresVerificationRetry(f.ctx, f.l, r)
		if attempt == api.PostgresCopyVerificationAttemptsMax {
			if !errors.Is(err, state.ErrQuotaExceeded) {
				t.Fatal("total attempt ceiling escaped", err)
			}
			break
		}
		if err != nil || !first || next.Attempt != attempt+1 {
			t.Fatal("consecutive next owner", err)
		}
		a = next
	}
	history, err := f.s.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(f.ctx, f.l, id, x.oid)
	if err != nil || len(history) != 3 {
		t.Fatal("history missing", err)
	}
	original, err := f.s.ProjectEnvironmentClonePostgresVerificationForLease(f.ctx, f.l, id, x.oid)
	if err != nil || original.State != "verifying" || original.VerificationID != history[0].VerificationID {
		t.Fatal("first owner overwritten", err)
	}
	// Native chronology is independent of CP clock, but the immediate predecessor
	// timestamps must remain exact. This corruption still satisfies SQL CHECKs.
	_, err = f.pool.Exec(f.ctx, `UPDATE project_environment_clone_postgres_verification_attempts SET previous_opened_at=previous_opened_at-interval '1 microsecond' WHERE verification_id=$1`, history[1].VerificationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(f.ctx, f.l, id, x.oid); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed native predecessor trusted", err)
	}
}

func TestPgClonePostgresVerificationAttemptsLeaseHandoffAndExpiryAfterLocks(t *testing.T) {
	x, r, _ := cloneFailedVerificationFixture(t)
	f := x.f
	id := r.Scope.SourceDatabaseID
	old := f.l
	if err := f.s.ReleaseProjectEnvironmentCloneLease(f.ctx, old, 0); err != nil {
		t.Fatal(err)
	}
	var err error
	f.l, err = f.s.ClaimNextProjectEnvironmentClone(f.ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerificationRetry(f.ctx, old, r); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker reserved", err)
	}
	a, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerificationRetry(f.ctx, f.l, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, "UPDATE project_environment_clone_operations SET lease_until=clock_timestamp()+interval '300 milliseconds' WHERE id=$1 RETURNING lease_until", f.l.Operation.ID).Scan(&f.l.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	lock, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	if _, err = lock.Exec(f.ctx, `SELECT 1 FROM project_environment_clone_postgres_verification_attempts WHERE verification_id=$1 FOR UPDATE`, a.VerificationID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := f.s.ClaimProjectEnvironmentClonePostgresVerificationAttempt(f.ctx, f.l, id, x.oid, a.VerificationID)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err = f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%project_environment_clone_postgres_verification_attempts%' AND pid<>pg_backend_pid())`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not wait on attempt")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	if err = lock.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired claim acquired attempt after lock wait", err)
	}
	var phase string
	if err = f.pool.QueryRow(f.ctx, "SELECT state FROM project_environment_clone_postgres_verification_attempts WHERE verification_id=$1", a.VerificationID).Scan(&phase); err != nil || phase != "reserved" {
		t.Fatal("expired mutation committed", err)
	}
	if _, err = f.s.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(f.ctx, f.l, id, x.oid); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired history read", err)
	}
}

func TestPgClonePostgresVerificationAttemptsMigrationRoundTripAndOwnedDownRefusal(t *testing.T) {
	up, down := cloneVerificationAttemptMigrationParts(t)
	_, ctx, pool := pgWithPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	shape := `SELECT jsonb_build_array((SELECT jsonb_agg(jsonb_build_array(attname,format_type(atttypid,atttypmod),attnotnull) ORDER BY attnum) FROM pg_attribute WHERE attrelid='project_environment_clone_postgres_verification_attempts'::regclass AND attnum>0 AND NOT attisdropped),(SELECT jsonb_agg(jsonb_build_array(conrelid::regclass::text,conname,pg_get_constraintdef(oid)) ORDER BY conrelid::regclass::text,conname) FROM pg_constraint WHERE conrelid IN ('project_environment_clone_postgres_verification_attempts'::regclass,'project_environment_clone_postgres_verifications'::regclass)))::text`
	var before, after string
	if err = tx.QueryRow(ctx, shape).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, shape).Scan(&after); err != nil || before != after {
		t.Fatal("round trip changed shape", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	x, r, _ := cloneFailedVerificationFixture(t)
	f := x.f
	for _, phase := range []string{"failed", "reserved", "verifying"} {
		if phase == "reserved" {
			_, _, err = f.s.ReserveProjectEnvironmentClonePostgresVerificationRetry(f.ctx, f.l, r)
		}
		if phase == "verifying" {
			history, e := f.s.ProjectEnvironmentClonePostgresVerificationAttemptsForLease(f.ctx, f.l, r.Scope.SourceDatabaseID, x.oid)
			if e != nil {
				t.Fatal(e)
			}
			_, _, err = f.s.ClaimProjectEnvironmentClonePostgresVerificationAttempt(f.ctx, f.l, r.Scope.SourceDatabaseID, x.oid, history[1].VerificationID)
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
		if !errors.As(downErr, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "postgres_verification_attempts_down_no_ownership" {
			t.Fatal("owned history allowed downgrade", phase, downErr)
		}
	}
}

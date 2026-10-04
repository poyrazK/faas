//go:build !no_pg

// adr:531
package state_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/state"
)

func cloneVerificationReadBudgetMigrationParts(t *testing.T) (string, string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261004095153303_environment_clone_postgres_verification_read_budgets.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("missing Down")
	}
	return parts[0], parts[1]
}
func verificationReadBudgetLimits() state.ProjectEnvironmentClonePostgresVerificationReadBudgetLimits {
	return state.ProjectEnvironmentClonePostgresVerificationReadBudgetLimits{Count: api.PostgresCopyContentsManifestsPerAccountMax, Bytes: api.PostgresCopyVerificationReadBytesPerAccountMax}
}
func cloneReadBudgetFixture(t *testing.T) (databaseSQLPinsFixture, state.ProjectEnvironmentClonePostgresVerificationRequest, state.ProjectEnvironmentClonePostgresVerificationReadBudgetRequest, copycontents.SealedMatch) {
	t.Helper()
	x, r, proof := cloneVerificationFixture(t)
	original, _, err := x.f.s.ReserveProjectEnvironmentClonePostgresVerification(x.f.ctx, x.f.l, r)
	if err != nil {
		t.Fatal(err)
	}
	b := state.ProjectEnvironmentClonePostgresVerificationReadBudgetRequest{Scope: r.Scope, DatabaseOID: x.oid, OriginalVerificationID: original.VerificationID, ReadBytes: 300, SortMemoryBytes: 128, SortDiskBytes: 256}
	return x, r, b, proof
}
func reserveReadBudget(t *testing.T, x databaseSQLPinsFixture, b state.ProjectEnvironmentClonePostgresVerificationReadBudgetRequest) state.ProjectEnvironmentClonePostgresVerificationReadBudget {
	t.Helper()
	got, _, err := x.f.s.ReserveProjectEnvironmentClonePostgresVerificationReadBudget(x.f.ctx, x.f.l, b, verificationReadBudgetLimits())
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func claimOriginalRead(t *testing.T, x databaseSQLPinsFixture, b state.ProjectEnvironmentClonePostgresVerificationReadBudgetRequest) state.ProjectEnvironmentClonePostgresVerificationReadRequest {
	t.Helper()
	_, _, err := x.f.s.ClaimProjectEnvironmentClonePostgresVerification(x.f.ctx, x.f.l, b.Scope.SourceDatabaseID, x.oid)
	if err != nil {
		t.Fatal(err)
	}
	return state.ProjectEnvironmentClonePostgresVerificationReadRequest{SourceDatabaseID: b.Scope.SourceDatabaseID, DatabaseOID: x.oid, VerificationID: b.OriginalVerificationID, Attempt: 1, ReadBytes: 100, SortMemoryBytes: 128, SortDiskBytes: 256}
}

// Explicit opaque SQL history for ownership accounting. This helper cannot
// manufacture native closure or data equality; APID tests qualify those.
func failOriginalReadLedger(t *testing.T, x databaseSQLPinsFixture, targetOID uint32) {
	t.Helper()
	_, err := x.f.pool.Exec(x.f.ctx, `INSERT INTO project_environment_clone_postgres_verification_attempts
 (operation_id,source_database_id,database_oid,original_verification_id,attempt,verification_id,key_id,reserved_bytes,state,created_at,request_started_at,window_opened_at,target_database_oid,native_closed_at,failed_at)
 SELECT operation_id,source_database_id,database_oid,verification_id,1,verification_id,key_id,reserved_bytes,'failed',created_at,request_started_at,clock_timestamp(),$2,clock_timestamp(),clock_timestamp()
 FROM project_environment_clone_postgres_verifications WHERE operation_id=$1`, x.f.l.Operation.ID, targetOID)
	if err != nil {
		t.Fatal(err)
	}
}
func claimRetryRead(t *testing.T, x databaseSQLPinsFixture, r state.ProjectEnvironmentClonePostgresVerificationRequest, previous string) (state.ProjectEnvironmentClonePostgresVerificationAttempt, state.ProjectEnvironmentClonePostgresVerificationReadRequest) {
	t.Helper()
	a, _, err := x.f.s.ReserveProjectEnvironmentClonePostgresVerificationRetry(x.f.ctx, x.f.l, state.ProjectEnvironmentClonePostgresVerificationRetryRequest{ProjectEnvironmentClonePostgresVerificationRequest: r, PreviousVerificationID: previous})
	if err != nil {
		t.Fatal(err)
	}
	a, _, err = x.f.s.ClaimProjectEnvironmentClonePostgresVerificationAttempt(x.f.ctx, x.f.l, r.Scope.SourceDatabaseID, x.oid, a.VerificationID)
	if err != nil {
		t.Fatal(err)
	}
	return a, state.ProjectEnvironmentClonePostgresVerificationReadRequest{SourceDatabaseID: r.Scope.SourceDatabaseID, DatabaseOID: x.oid, VerificationID: a.VerificationID, Attempt: a.Attempt, ReadBytes: 100, SortMemoryBytes: 128, SortDiskBytes: 256}
}

func TestPgClonePostgresVerificationReadBudgetConcurrentReservationDebitAndReplay(t *testing.T) {
	x, _, b, _ := cloneReadBudgetFixture(t)
	f := x.f
	type result struct {
		b     state.ProjectEnvironmentClonePostgresVerificationReadBudget
		first bool
		err   error
	}
	run := func(r *state.ProjectEnvironmentClonePostgresVerificationReadRequest) state.ProjectEnvironmentClonePostgresVerificationReadBudget {
		results := make(chan result, 6)
		var wg sync.WaitGroup
		for range 6 {
			wg.Go(func() {
				var v state.ProjectEnvironmentClonePostgresVerificationReadBudget
				var first bool
				var err error
				if r == nil {
					v, first, err = f.s.ReserveProjectEnvironmentClonePostgresVerificationReadBudget(f.ctx, f.l, b, verificationReadBudgetLimits())
				} else {
					v, first, err = f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, *r)
				}
				results <- result{v, first, err}
			})
		}
		wg.Wait()
		close(results)
		var held state.ProjectEnvironmentClonePostgresVerificationReadBudget
		firsts := 0
		for got := range results {
			if got.err != nil {
				t.Fatal(got.err)
			}
			if held.OriginalVerificationID == "" {
				held = got.b
			}
			if !reflect.DeepEqual(held, got.b) {
				t.Fatal("concurrent call changed hold")
			}
			if got.first {
				firsts++
			}
		}
		if firsts != 1 {
			t.Fatal("not one mutation", firsts)
		}
		return held
	}
	held := run(nil)
	r := claimOriginalRead(t, x, b)
	debited := run(&r)
	if !held.CreatedAt.Equal(debited.CreatedAt) || debited.AllocatedReadBytes != 100 || len(debited.Allocations) != 1 {
		t.Fatal("wrong debit")
	}
	debited.Allocations[0].ReadBytes = 999
	replay, first, err := f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, r)
	if err != nil || first || replay.AllocatedReadBytes != 100 || replay.Allocations[0].ReadBytes != 100 {
		t.Fatal("replay changed ledger", err)
	}
	for _, mode := range []string{"read", "memory", "disk", "owner"} {
		t.Run(mode, func(t *testing.T) {
			changed := r
			switch mode {
			case "read":
				changed.ReadBytes--
			case "memory":
				changed.SortMemoryBytes--
			case "disk":
				changed.SortDiskBytes--
			case "owner":
				changed.VerificationID = uuid.NewString()
			}
			v, first, err := f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, changed)
			if !errors.Is(err, state.ErrConflict) || first || v.OriginalVerificationID != "" {
				t.Fatal("changed replay accepted", err)
			}
		})
	}
	var count, bytes int64
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*),sum(read_bytes) FROM project_environment_clone_postgres_verification_read_budgets WHERE account_id=$1`, b.Scope.AccountID).Scan(&count, &bytes); err != nil || count != 1 || bytes != 300 {
		t.Fatal("account lost charged aggregate", err)
	}
}

func TestPgClonePostgresVerificationReadBudgetRetainsAllThreeAttemptDebits(t *testing.T) {
	x, r, b, proof := cloneReadBudgetFixture(t)
	f := x.f
	reserveReadBudget(t, x, b)
	read := claimOriginalRead(t, x, b)
	if _, _, err := f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, read); err != nil {
		t.Fatal(err)
	}
	failOriginalReadLedger(t, x, proof.TargetDatabaseOID)
	originalRead := read
	previous := read.VerificationID
	for attempt := int32(2); attempt <= 3; attempt++ {
		a, next := claimRetryRead(t, x, r, previous)
		// A valid latest owner still cannot consume somebody else's ordinal.
		skipped := next
		skipped.Attempt--
		if _, _, err := f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, skipped); !errors.Is(err, state.ErrConflict) {
			t.Fatal("wrong ordinal spent credits", err)
		}
		got, first, err := f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, next)
		if err != nil || !first || got.AllocatedReadBytes != int64(attempt)*100 || len(got.Allocations) != int(attempt) {
			t.Fatal("lost failed debit", err)
		}
		failVerificationLedgerAttempt(t, x, a, proof.TargetDatabaseOID)
		previous = a.VerificationID
		read = next
	}
	got, err := f.s.ProjectEnvironmentClonePostgresVerificationReadBudgetForLease(f.ctx, f.l, b.Scope.SourceDatabaseID, x.oid)
	if err != nil || got.AllocatedReadBytes != 300 {
		t.Fatal("closed work refunded", err)
	}
	for _, old := range []state.ProjectEnvironmentClonePostgresVerificationReadRequest{originalRead, read} {
		replay, first, err := f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, old)
		if err != nil || first || !reflect.DeepEqual(replay, got) {
			t.Fatal("old debit replay lost", err)
		}
	}
	if _, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerificationRetry(f.ctx, f.l, state.ProjectEnvironmentClonePostgresVerificationRetryRequest{ProjectEnvironmentClonePostgresVerificationRequest: r, PreviousVerificationID: previous}); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatal("fourth attempt admitted", err)
	}
	held := reserveReadBudget(t, x, b)
	if !reflect.DeepEqual(held, got) {
		t.Fatal("reservation replay reset spend")
	}
}

func TestPgClonePostgresVerificationReadBudgetExhaustionAndSortCaps(t *testing.T) {
	x, r, b, proof := cloneReadBudgetFixture(t)
	f := x.f
	b.ReadBytes = 199
	limits := verificationReadBudgetLimits()
	limits.Bytes = 198
	if _, first, err := f.s.ReserveProjectEnvironmentClonePostgresVerificationReadBudget(f.ctx, f.l, b, limits); !errors.Is(err, state.ErrQuotaExceeded) || first {
		t.Fatal("account credits exceeded", err)
	}
	reserveReadBudget(t, x, b)
	read := claimOriginalRead(t, x, b)
	for _, mode := range []string{"memory", "disk"} {
		changed := read
		if mode == "memory" {
			changed.SortMemoryBytes++
		} else {
			changed.SortDiskBytes++
		}
		if _, _, err := f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, changed); !errors.Is(err, state.ErrQuotaExceeded) {
			t.Fatal("sort hold exceeded", mode, err)
		}
	}
	if _, _, err := f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, read); err != nil {
		t.Fatal(err)
	}
	failOriginalReadLedger(t, x, proof.TargetDatabaseOID)
	_, next := claimRetryRead(t, x, r, read.VerificationID)
	if _, _, err := f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, next); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatal("aggregate budget reset by retry", err)
	}
	next.ReadBytes = 99
	got, _, err := f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, next)
	if err != nil || got.AllocatedReadBytes != 199 {
		t.Fatal("remaining credits unavailable", err)
	}
	changed := b
	changed.ReadBytes++
	if _, _, err = f.s.ReserveProjectEnvironmentClonePostgresVerificationReadBudget(f.ctx, f.l, changed, verificationReadBudgetLimits()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("in-place credit expansion", err)
	}
}

func TestPgClonePostgresVerificationReadBudgetCannotBackfillOrSkipUnknownWork(t *testing.T) {
	for _, mode := range []string{"legacy", "skip", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			x, r, b, proof := cloneReadBudgetFixture(t)
			f := x.f
			if mode != "legacy" {
				reserveReadBudget(t, x, b)
			}
			read := claimOriginalRead(t, x, b)
			if mode == "legacy" {
				if _, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerificationReadBudget(f.ctx, f.l, b, verificationReadBudgetLimits()); !errors.Is(err, state.ErrConflict) {
					t.Fatal("retroactive admission", err)
				}
				return
			}
			if mode == "advanced" {
				proof.OwnerID = read.VerificationID
				if _, err := f.s.RecordProjectEnvironmentClonePostgresVerificationMatch(f.ctx, f.l, b.Scope.SourceDatabaseID, x.oid, proof); err != nil {
					t.Fatal(err)
				}
				if _, _, err := f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, read); !errors.Is(err, state.ErrConflict) {
					t.Fatal("compared owner acquired new read", err)
				}
				return
			}
			failOriginalReadLedger(t, x, proof.TargetDatabaseOID)
			_, next := claimRetryRead(t, x, r, read.VerificationID)
			for _, request := range []state.ProjectEnvironmentClonePostgresVerificationReadRequest{read, next} {
				if _, _, err := f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, request); !errors.Is(err, state.ErrConflict) {
					t.Fatal("unaccounted original dispatch backfilled", err)
				}
			}
		})
	}
}

func TestPgClonePostgresVerificationReadBudgetChangedParentsAndLedgerRejected(t *testing.T) {
	for _, mode := range []string{"scope", "owner", "parent", "time", "account", "spend", "gap"} {
		t.Run(mode, func(t *testing.T) {
			x, r, b, proof := cloneReadBudgetFixture(t)
			f := x.f
			reserveReadBudget(t, x, b)
			if mode == "scope" || mode == "owner" {
				changed := b
				if mode == "scope" {
					changed.Scope.SourceDataResourceID += "-changed"
				} else {
					changed.OriginalVerificationID = uuid.NewString()
				}
				if _, _, err := f.s.ReserveProjectEnvironmentClonePostgresVerificationReadBudget(f.ctx, f.l, changed, verificationReadBudgetLimits()); !errors.Is(err, state.ErrConflict) {
					t.Fatal("changed identity accepted", err)
				}
				return
			}
			read := claimOriginalRead(t, x, b)
			if _, _, err := f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, read); err != nil {
				t.Fatal(err)
			}
			var err error
			switch mode {
			case "parent":
				_, err = f.pool.Exec(f.ctx, `UPDATE project_environment_clone_postgres_verifications SET manifest_fingerprint=$2 WHERE verification_id=$1`, b.OriginalVerificationID, strings.Repeat("b", 64))
			case "time":
				_, err = f.pool.Exec(f.ctx, `UPDATE project_environment_clone_postgres_verification_read_debits SET created_at=created_at-interval '1 day' WHERE verification_id=$1`, b.OriginalVerificationID)
			case "spend":
				_, err = f.pool.Exec(f.ctx, `UPDATE project_environment_clone_postgres_verification_read_debits SET read_bytes=301 WHERE verification_id=$1`, b.OriginalVerificationID)
			case "gap":
				failOriginalReadLedger(t, x, proof.TargetDatabaseOID)
				_, next := claimRetryRead(t, x, r, read.VerificationID)
				if _, _, err = f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, next); err != nil {
					t.Fatal(err)
				}
				_, err = f.pool.Exec(f.ctx, `DELETE FROM project_environment_clone_postgres_verification_read_debits WHERE verification_id=$1`, read.VerificationID)
			}
			if mode == "account" {
				other, e := f.s.CreateAccount(f.ctx, "read-budget-other@example.com", api.PlanPro)
				if e != nil {
					t.Fatal(e)
				}
				_, err = f.pool.Exec(f.ctx, `UPDATE project_environment_clone_postgres_verification_read_budgets SET account_id=$2 WHERE original_verification_id=$1`, b.OriginalVerificationID, other.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.s.ProjectEnvironmentClonePostgresVerificationReadBudgetForLease(f.ctx, f.l, b.Scope.SourceDatabaseID, x.oid)
			if !errors.Is(err, state.ErrConflict) || got.OriginalVerificationID != "" {
				t.Fatal("corrupt budget trusted", err)
			}
		})
	}
}

func TestPgClonePostgresVerificationReadBudgetHandoffAndExpiryAfterLock(t *testing.T) {
	x, _, b, _ := cloneReadBudgetFixture(t)
	f := x.f
	held := reserveReadBudget(t, x, b)
	read := claimOriginalRead(t, x, b)
	old := f.l
	if err := f.s.ReleaseProjectEnvironmentCloneLease(f.ctx, old, 0); err != nil {
		t.Fatal(err)
	}
	var err error
	f.l, err = f.s.ClaimNextProjectEnvironmentClone(f.ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, old, read); !errors.Is(err, state.ErrConflict) {
		t.Fatal("old lease debited", err)
	}
	got, err := f.s.ProjectEnvironmentClonePostgresVerificationReadBudgetForLease(f.ctx, f.l, b.Scope.SourceDatabaseID, x.oid)
	if err != nil || !reflect.DeepEqual(got, held) {
		t.Fatal("handoff replaced credits", err)
	}
	if err = f.pool.QueryRow(f.ctx, `UPDATE project_environment_clone_operations SET lease_until=clock_timestamp()+interval '300 milliseconds' WHERE id=$1 RETURNING lease_until`, f.l.Operation.ID).Scan(&f.l.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	lock, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	if _, err = lock.Exec(f.ctx, `SELECT 1 FROM project_environment_clone_postgres_verification_read_budgets WHERE original_verification_id=$1 FOR UPDATE`, b.OriginalVerificationID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(f.ctx, f.l, read)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err = f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%project_environment_clone_postgres_verification_read_budgets%' AND pid<>pg_backend_pid())`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no budget lock wait")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	if err = lock.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired wait spent credits", err)
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM project_environment_clone_postgres_verification_read_debits`).Scan(&count); err != nil || count != 0 {
		t.Fatal("expired debit committed", err)
	}
}

func TestPgClonePostgresVerificationReadBudgetMigrationRoundTripAndOwnedDownRefusal(t *testing.T) {
	up, down := cloneVerificationReadBudgetMigrationParts(t)
	_, ctx, pool := pgWithPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	shape := `SELECT jsonb_build_array((SELECT jsonb_agg(jsonb_build_array(attrelid::regclass::text,attname,format_type(atttypid,atttypmod),attnotnull) ORDER BY attrelid::regclass::text,attnum) FROM pg_attribute WHERE attrelid IN ('project_environment_clone_postgres_verification_read_budgets'::regclass,'project_environment_clone_postgres_verification_read_debits'::regclass) AND attnum>0 AND NOT attisdropped),(SELECT jsonb_agg(jsonb_build_array(conrelid::regclass::text,conname,pg_get_constraintdef(oid)) ORDER BY conrelid::regclass::text,conname) FROM pg_constraint WHERE conrelid IN ('project_environment_clone_postgres_verification_read_budgets'::regclass,'project_environment_clone_postgres_verification_read_debits'::regclass)))::text`
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
		t.Fatal("schema round trip changed", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	x, _, b, _ := cloneReadBudgetFixture(t)
	reserveReadBudget(t, x, b)
	for _, phase := range []string{"reserved", "allocated"} {
		if phase == "allocated" {
			read := claimOriginalRead(t, x, b)
			if _, _, err = x.f.s.AllocateProjectEnvironmentClonePostgresVerificationRead(x.f.ctx, x.f.l, read); err != nil {
				t.Fatal(err)
			}
		}
		tx, err = x.f.pool.Begin(x.f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, downErr := tx.Exec(x.f.ctx, down)
		_ = tx.Rollback(x.f.ctx)
		var pgErr *pgconn.PgError
		if !errors.As(downErr, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "postgres_verification_read_budgets_down_no_ownership" {
			t.Fatal("owned credit lost during downgrade", phase, downErr)
		}
	}
}

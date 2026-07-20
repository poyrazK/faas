package state_test

// PgStore parity tests for the three new Store methods added in PR #69
// (audit findings #1 + #2):
//   - LoadAndStampLastQuotaWarning — paid-tier quota_warning dedupe
//     gate (spec §4.7).
//   - ClearQuotaWarning — apid's invoice.payment_succeeded arming
//     path; cancels the stamp so a paying customer doesn't get
//     skipped tomorrow because of yesterday's stamp.
//   - MarkDunningStep — the dunning state machine's
//     compare-and-advance primitive (spec §4.7, §17 dunning state
//     machine).
//
// MemStore parity lives in pkg/state/load_and_stamp_quota_warning_test.go.
// This file pins the hand-written SQL against a real cluster
// (`pgstore.go::LoadAndStamp/ClearQuotaWarning/MarkDunningStep`).
// Skips on FAAS_SKIP_PG_TESTS and on no Postgres.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgWithPool re-opens an isolated pool (the PgStore does not expose
// its connection pool — by design, so the rest of the codebase must
// not reach around the Store interface). Mirrors
// pkg/state/pgstore_account_deletion_test.go::pgStoreAccountDeletionWithPool.
func pgWithPool(t *testing.T) (*state.PgStore, context.Context, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return state.NewPgStore(pool), ctx, pool
}

// pgTestEmail returns a unique email per test so parallel runs do not
// collide on the unique index.
func pgTestEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-pg@example.com", t.Name())
}

// TestPg_LoadAndStampLastQuotaWarning_DedupesPerDay pins the atomic
// compare-and-set that lets pkg/meter.EnforceQuota emit at most one
// paid-tier `quota_warning` pg_notify per UTC day. Mirrors the
// MemStore case at pkg/state/load_and_stamp_quota_warning_test.go:24,
// but here against the real UPDATE…RETURNING SQL.
func TestPg_LoadAndStampLastQuotaWarning_DedupesPerDay(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, pgTestEmail(t), api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// Anchor on a known UTC morning so the date arithmetic is
	// easy to reason about.
	day1 := time.Date(2026, 7, 20, 9, 47, 12, 0, time.UTC)
	day2 := day1.Add(25 * time.Hour) // next UTC day

	// First call of day 1 — emits, stamps.
	already, err := s.LoadAndStampLastQuotaWarning(ctx, acct.ID, day1)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if already {
		t.Fatal("first call already = true (want false): the column was supposed to be NULL")
	}
	got, err := s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastQuotaWarningAt == nil {
		t.Fatal("stamp = nil after first call")
	}
	want := day1.UTC().Truncate(24 * time.Hour)
	if !got.LastQuotaWarningAt.Equal(want) {
		t.Errorf("stamp = %s, want %s (UTC midnight of day1)", got.LastQuotaWarningAt, want)
	}

	// Same-day repeat — must be a no-op.
	already, err = s.LoadAndStampLastQuotaWarning(ctx, acct.ID, day1)
	if err != nil {
		t.Fatalf("same-day repeat: %v", err)
	}
	if !already {
		t.Fatal("same-day repeat already = false (want true): the SQL UPDATE…RETURNING is wrong")
	}
	got, err = s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastQuotaWarningAt.Equal(want) {
		t.Errorf("same-day stamp changed: got %s, want %s", got.LastQuotaWarningAt, want)
	}

	// Next day — fresh stamp, already=false.
	already, err = s.LoadAndStampLastQuotaWarning(ctx, acct.ID, day2)
	if err != nil {
		t.Fatalf("next-day call: %v", err)
	}
	if already {
		t.Fatal("next-day already = true (want false): the stamp should advance")
	}
	got, err = s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	want2 := day2.UTC().Truncate(24 * time.Hour)
	if !got.LastQuotaWarningAt.Equal(want2) {
		t.Errorf("next-day stamp = %s, want %s", got.LastQuotaWarningAt, want2)
	}
}

// TestPg_LoadAndStampLastQuotaWarning_UnknownID covers the ErrNotFound
// path (a deleted account, or a webhook pointing at a row that was
// hard-deleted between lookup and stamp). PgStore does the conversion
// at pgstore.go::LoadAndStampLastQuotaWarning (pgx.ErrNoRows → state.ErrNotFound).
func TestPg_LoadAndStampLastQuotaWarning_UnknownID(t *testing.T) {
	s, ctx := pgStore(t)
	// accounts.id is a uuid — pass a syntactically valid UUID that
	// doesn't correspond to any row, so the SQL exercises the
	// `WHERE id = $1` no-match branch (pgx.ErrNoRows → state.ErrNotFound)
	// rather than crashing on a parse error.
	already, err := s.LoadAndStampLastQuotaWarning(ctx, uuid.New().String(), time.Now())
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if already {
		t.Error("already = true on unknown id (want false)")
	}
}

// TestPg_ClearQuotaWarning_ResetsStamp covers the apid webhook's
// invoice.payment_succeeded path: a customer whose stamp is cleared
// must see a fresh warning on the next over-quota tick.
func TestPg_ClearQuotaWarning_ResetsStamp(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, pgTestEmail(t), api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	day := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	if _, err := s.LoadAndStampLastQuotaWarning(ctx, acct.ID, day); err != nil {
		t.Fatalf("seed stamp: %v", err)
	}
	got, err := s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastQuotaWarningAt == nil {
		t.Fatal("stamp = nil after LoadAndStamp")
	}
	if err := s.ClearQuotaWarning(ctx, acct.ID); err != nil {
		t.Fatalf("ClearQuotaWarning: %v", err)
	}
	got, err = s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastQuotaWarningAt != nil {
		t.Errorf("stamp = %s after Clear, want nil", got.LastQuotaWarningAt)
	}

	// Clearing twice is idempotent (no error).
	if err := s.ClearQuotaWarning(ctx, acct.ID); err != nil {
		t.Errorf("ClearQuotaWarning idempotent: %v", err)
	}
	// Clearing an unknown id is a silent no-op — mirrors the apid
	// webhook's "customer lookup may have returned a row that's since
	// been deleted" reality. Use a valid-format UUID that doesn't
	// correspond to any row (the column type is uuid; a non-UUID
	// string would surface as a parse error, not a no-match).
	if err := s.ClearQuotaWarning(ctx, uuid.New().String()); err != nil {
		t.Errorf("ClearQuotaWarning unknown id: %v, want nil", err)
	}

	// After clear, a same-day LoadAndStamp emits again (already=false).
	already, err := s.LoadAndStampLastQuotaWarning(ctx, acct.ID, day)
	if err != nil {
		t.Fatalf("post-clear LoadAndStamp: %v", err)
	}
	if already {
		t.Error("post-clear already = true (want false): Clear did not actually null the stamp")
	}
}

// TestPg_MarkDunningStep_PastDueStampsPastDueAt is the SQL-side
// coverage for the dunning timer's compare-and-flip primitive.
// Mirrors pkg/state/load_and_stamp_quota_warning_test.go:140
// (MemStore parity):
//   - past_due → past_due backfills past_due_at (the legacy-row path).
//   - past_due → suspended advances the status, preserves past_due_at.
//   - suspended → deleted_pending advances, preserves past_due_at.
//   - Wrong from-status or unknown id → ErrNotFound.
func TestPg_MarkDunningStep_PastDueStampsPastDueAt(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, pgTestEmail(t), api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// Flip active → past_due via MarkDunningStep (the path the apid
	// webhook now uses, see handlers_ext.go invoice.payment_failed).
	if err := s.MarkDunningStep(ctx, acct.ID, state.AccountActive, state.AccountPastDue); err != nil {
		t.Fatalf("active → past_due: %v", err)
	}
	got, err := s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.AccountPastDue {
		t.Errorf("status = %s, want past_due", got.Status)
	}
	if got.PastDueAt == nil {
		t.Fatal("PastDueAt = nil after fresh active → past_due (the webhook did not stamp)")
	}
	stamp := *got.PastDueAt

	// past_due → suspended: status advances, past_due_at preserved.
	if err := s.MarkDunningStep(ctx, acct.ID, state.AccountPastDue, state.AccountSuspended); err != nil {
		t.Fatalf("past_due → suspended: %v", err)
	}
	got, err = s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.AccountSuspended {
		t.Errorf("status = %s, want suspended", got.Status)
	}
	if got.PastDueAt == nil || !got.PastDueAt.Equal(stamp) {
		t.Errorf("PastDueAt changed during past_due → suspended: got %s, want %s",
			got.PastDueAt, stamp)
	}

	// suspended → deleted_pending: status advances, past_due_at preserved.
	if err := s.MarkDunningStep(ctx, acct.ID, state.AccountSuspended, state.AccountDeletedPending); err != nil {
		t.Fatalf("suspended → deleted_pending: %v", err)
	}
	got, err = s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.AccountDeletedPending {
		t.Errorf("status = %s, want deleted_pending", got.Status)
	}
	if got.PastDueAt == nil || !got.PastDueAt.Equal(stamp) {
		t.Errorf("PastDueAt changed during suspended → deleted_pending: got %s, want %s",
			got.PastDueAt, stamp)
	}

	// Compare-and-flip guard: wrong from-status → ErrNotFound.
	if err := s.MarkDunningStep(ctx, acct.ID, state.AccountActive, state.AccountSuspended); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("wrong from-status: err = %v, want ErrNotFound", err)
	}
	// Unknown id → ErrNotFound. accounts.id is a uuid so pass a
	// valid-format UUID that doesn't exist — the WHERE clause won't
	// match and RowsAffected() == 0 returns ErrNotFound.
	if err := s.MarkDunningStep(ctx, uuid.New().String(), state.AccountActive, state.AccountPastDue); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("unknown id: err = %v, want ErrNotFound", err)
	}
}

// TestPg_MarkDunningStep_BackfillPastDueAtNoOpFlip exercises the
// legacy-row data-integrity path. A pre-migration-00013 account in
// past_due with past_due_at=NULL gets a backfilled stamp via
// MarkDunningStep(past_due, past_due), without changing the status.
// The dunning timer relies on this to heal accounts that entered
// past_due before the migration column existed.
func TestPg_MarkDunningStep_BackfillPastDueAtNoOpFlip(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	acct, err := s.CreateAccount(ctx, pgTestEmail(t), api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	// Force status=past_due without a past_due_at stamp by hand-editing
	// both columns directly. Production never reaches this shape —
	// MarkDunningStep stamps PastDueAt whenever it transitions into
	// past_due — but an account that entered past_due before migration
	// 00013 could land here.
	if _, err := pool.Exec(ctx,
		`update accounts set status = 'past_due', past_due_at = null where id = $1`, acct.ID); err != nil {
		t.Fatalf("seed legacy shape: %v", err)
	}
	got, err := s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.AccountPastDue || got.PastDueAt != nil {
		t.Fatalf("setup: got %+v, want {Status: past_due, PastDueAt: nil}", got)
	}

	// No-op flip → status stays past_due, PastDueAt gets stamped.
	if err := s.MarkDunningStep(ctx, acct.ID, state.AccountPastDue, state.AccountPastDue); err != nil {
		t.Fatalf("backfill flip: %v", err)
	}
	got, err = s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.AccountPastDue {
		t.Errorf("status = %s, want past_due (backfill must not transition)", got.Status)
	}
	if got.PastDueAt == nil {
		t.Error("PastDueAt = nil after backfill flip (the SQL coalesce did not write the stamp)")
	}
}

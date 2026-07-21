// TestLoadAndStampLastQuotaWarning pins the dedupe gate that keeps
// pkg/meter.EnforceQuota from emitting more than one paid-tier
// quota_warning pg_notify per UTC day (spec §4.7, audit finding #1).
//
// MemStore parity (plan R4) — PgStore tests are in
// pkg/state/pgstore_account_quota_warning_test.go when a pgtest.Open
// fixture is available; on a machine without Postgres they're skipped.
//
// Cases:
//
//   - First call today    → (false, nil). Stamp = today's UTC midnight.
//   - Same-day repeat     → (true, nil).  Stamp unchanged.
//   - Next-day call       → (false, nil). Stamp = next UTC midnight.
//   - Missing account id  → ErrNotFound.
package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLoadAndStampLastQuotaWarning_DedupesPerDay(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	acct, err := s.CreateAccount(ctx, "alice@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}

	// Anchor on a known UTC morning so the date arithmetic is
	// easy to verify.
	day1 := time.Date(2026, 7, 20, 9, 47, 12, 0, time.UTC) // mid-morning
	day2 := day1.Add(25 * time.Hour)                       // next UTC day

	// First call of day 1 — emits the warning, stamps the column.
	already, err := s.LoadAndStampLastQuotaWarning(ctx, acct.ID, day1)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if already {
		t.Fatalf("first call already = true (want false): the column was supposed to be NULL")
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

	// Same-day repeat — must be a no-op (the gate at quota.go breaks
	// before the notify call).
	already, err = s.LoadAndStampLastQuotaWarning(ctx, acct.ID, day1)
	if err != nil {
		t.Fatalf("same-day repeat: %v", err)
	}
	if !already {
		t.Fatal("same-day repeat already = false (want true): dedupe gate is broken")
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
		t.Fatal("next-day already = true (want false): stamp should advance")
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

// TestClearQuotaWarning covers the apid webhook's invoice.payment_succeeded
// path: after a successful payment, the dedupe stamp must be cleared so
// the next quota tick (if the customer is still over quota) emits a
// fresh warning rather than being skipped.
func TestClearQuotaWarning(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	acct, err := s.CreateAccount(ctx, "bob@example.com", "hobby")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	if _, err := s.LoadAndStampLastQuotaWarning(ctx, acct.ID, day); err != nil {
		t.Fatal(err)
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
	// Clearing an unknown id is a silent no-op (matches the apid
	// webhook pattern where the Customer ID lookup may have returned
	// a row that's since been deleted).
	if err := s.ClearQuotaWarning(ctx, "missing"); err != nil {
		t.Errorf("ClearQuotaWarning unknown id: %v, want nil", err)
	}
}

// TestMarkDunningStep covers the past_due → suspended and suspended →
// deleted_pending transitions driven by pkg/meter.Dunning. Mirrors the
// MarkAccountDeletionPending pattern (compare-and-flip + ErrNotFound).
func TestMarkDunningStep(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	acct, err := s.CreateAccount(ctx, "carol@example.com", "hobby")
	if err != nil {
		t.Fatal(err)
	}
	// Flip to past_due manually + verify past_due_at stamped.
	if err := s.UpdateAccountStatus(ctx, acct.ID, AccountPastDue); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDunningStep(ctx, acct.ID, AccountPastDue, AccountPastDue); err != nil {
		t.Fatalf("backfill stamp via no-op flip: %v", err)
	}
	got, err := s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PastDueAt == nil {
		t.Fatal("PastDueAt = nil after no-op flip backfill")
	}
	pastDueAt := *got.PastDueAt

	// past_due → suspended advances the status, preserves past_due_at.
	if err := s.MarkDunningStep(ctx, acct.ID, AccountPastDue, AccountSuspended); err != nil {
		t.Fatalf("past_due → suspended: %v", err)
	}
	got, err = s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != AccountSuspended {
		t.Errorf("status = %s, want suspended", got.Status)
	}
	if got.PastDueAt == nil || !got.PastDueAt.Equal(pastDueAt) {
		t.Errorf("PastDueAt changed during past_due→suspended: got %s, want %s", got.PastDueAt, pastDueAt)
	}

	// suspended → deleted_pending.
	if err := s.MarkDunningStep(ctx, acct.ID, AccountSuspended, AccountDeletedPending); err != nil {
		t.Fatalf("suspended → deleted_pending: %v", err)
	}
	got, err = s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != AccountDeletedPending {
		t.Errorf("status = %s, want deleted_pending", got.Status)
	}

	// Wrong from-status → ErrNotFound (compare-and-flip guard).
	if err := s.MarkDunningStep(ctx, acct.ID, AccountPastDue, AccountSuspended); !errors.Is(err, ErrNotFound) {
		t.Errorf("wrong from-status: err = %v, want ErrNotFound", err)
	}

	// Unknown id → ErrNotFound.
	if err := s.MarkDunningStep(ctx, "missing", AccountPastDue, AccountSuspended); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: err = %v, want ErrNotFound", err)
	}

	// Same from/to → no-op for non-past_due transitions (returns nil
	// without stamping anything). For past_due → past_due the call
	// is the backfill-stamp path used by pkg/meter.Dunning; it
	// succeeds and stamps PastDueAt. We don't test that here — it
	// is exercised by pkg/meter/dunning_test.go.
	if err := s.MarkDunningStep(ctx, acct.ID, AccountDeletedPending, AccountDeletedPending); err != nil {
		t.Errorf("from==to (deleted_pending): err = %v, want nil", err)
	}
}

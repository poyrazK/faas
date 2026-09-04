// Reducer tests for issue #279 PR-C.
//
// The reducer is a thin orchestrator on top of state.Store:
//
//   ConsumeCreditsForInvoice(ctx, store, invoiceID, actor, reason)
//       → GetInvoiceByID
//       → ComputeInvoiceOverageCents  (sum of usage_minutes → cents)
//       → ConsumeAccountCredit        (FIFO drain + ledger insert)
//
// These tests use MemStore — parity with PgStore is the
// pgstore_account_credits_test.go contract. The reducer does no DB
// work itself; the tests are pure logic over the Store seam.

package billing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestComputeInvoiceOverageCents_Floor pins the integer-cents math shared by
// the invoice reducer and the account overage-cap reader. The plan allowance
// is removed once from the account aggregate, then each remaining GB-hour is
// one cent. Fractional cents are floored and no float is used near money.
func TestComputeInvoiceOverageCents_Floor(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "alice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	// Anchor the period on UTC midnight so usage rows outside the
	// window are excluded (the window is [PeriodStart, PeriodEnd)).
	periodStart := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	inv := state.Invoice{
		ID:                uuid.NewString(),
		AccountID:         acct.ID,
		Provider:          "stripe",
		ProviderInvoiceID: "in_test_001",
		PeriodStart:       periodStart,
		PeriodEnd:         periodEnd,
	}
	store.SeedInvoiceForTest(inv)

	// Outside the window — must NOT contribute.
	if err := store.AppendUsage(ctx, acct.ID, "app-prior", "inst-prior",
		periodStart.Add(-time.Hour), 100_000_000, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("prior-month usage: %v", err)
	}

	// Inside the window — 50 included GB-hours plus 3 billable GB-hours.
	if err := store.AppendUsage(ctx, acct.ID, "app-1", "inst-1",
		periodStart.Add(2*time.Hour), 53*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("mid-month usage: %v", err)
	}

	got, err := ComputeInvoiceOverageCents(ctx, store, inv)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	const wantCents = int64(3)
	if got != wantCents {
		t.Fatalf("got %d cents, want %d", got, wantCents)
	}

	// Add half a GB-hour. The total is 53.5 GB-hours, which still floors
	// to 3 cents of overage.
	inv2 := inv
	inv2.ID = uuid.NewString()
	inv2.ProviderInvoiceID = "in_test_002"
	if err := store.AppendUsage(ctx, acct.ID, "app-2", "inst-2",
		periodStart.Add(3*time.Hour), api.SecondsPerGBHour/2, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("floor usage: %v", err)
	}
	store.SeedInvoiceForTest(inv2)
	got2, err := ComputeInvoiceOverageCents(ctx, store, inv2)
	if err != nil {
		t.Fatalf("compute 2: %v", err)
	}
	const wantCentsFloor = int64(3)
	if got2 != wantCentsFloor {
		t.Fatalf("got %d cents, want %d (floor under 1)", got2, wantCentsFloor)
	}
}

// TestConsumeCreditsForInvoice_EndToEnd exercises the full reducer:
//
//  1. Three credits seeded at distinct CreatedAt (FIFO order).
//  2. Invoice with PeriodStart/PeriodEnd and provider_invoice_id.
//  3. Usage planted to drive a known overage target.
//  4. ConsumeCreditsForInvoice drains credits, returns PerCredit,
//     RemainingCreditsCents, and stamps provider_invoice_id on each
//     ledger row.
//  5. Idempotency: re-running with the same provider_invoice_id
//     returns AlreadyConsumedForInvoice=true and the SAME
//     ConsumedCents, with no new ledger rows.
func TestConsumeCreditsForInvoice_EndToEnd(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "alice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	// Three credits: oldest 100, mid 200, newest 300 cents. FIFO will
	// drain oldest first.
	oldest, err := store.CreateAccountCredit(ctx, state.AccountCredit{
		AccountID: acct.ID, CentsRemaining: 100, Reason: "oldest",
	})
	if err != nil {
		t.Fatalf("oldest credit: %v", err)
	}
	// MemStore uses uuid-parsed CreatedAt from time.Now() — sleep
	// a hair to guarantee the next row has a strictly later
	// CreatedAt (MemStore's ListActiveCreditsForConsumption sorts
	// ascending by CreatedAt). On slow test runners the two
	// CreateAccountCredit calls can land in the same nanosecond and
	// sort order becomes flaky.
	time.Sleep(2 * time.Millisecond)
	mid, err := store.CreateAccountCredit(ctx, state.AccountCredit{
		AccountID: acct.ID, CentsRemaining: 200, Reason: "mid",
	})
	if err != nil {
		t.Fatalf("mid credit: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	newest, err := store.CreateAccountCredit(ctx, state.AccountCredit{
		AccountID: acct.ID, CentsRemaining: 300, Reason: "newest",
	})
	if err != nil {
		t.Fatalf("newest credit: %v", err)
	}

	// Invoice covers July 2026, target = 250 cents of overage.
	periodStart := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	inv := state.Invoice{
		ID:                uuid.NewString(),
		AccountID:         acct.ID,
		Provider:          "stripe",
		ProviderInvoiceID: "in_e2e_001",
		PeriodStart:       periodStart,
		PeriodEnd:         periodEnd,
	}
	store.SeedInvoiceForTest(inv)

	// Plant 50 included GB-hours plus 250 billable GB-hours.
	if err := store.AppendUsage(ctx, acct.ID, "app-1", "inst-1",
		periodStart.Add(24*time.Hour), 300*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("usage: %v", err)
	}

	// First call — drains 100 + 150 from oldest + mid, leaves 50 in
	// mid and the full 300 in newest untouched.
	res, gotInv, err := ConsumeCreditsForInvoice(ctx, store, inv.ID, "apid", "first run")
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if gotInv.ID != inv.ID {
		t.Fatalf("inv.ID = %s, want %s", gotInv.ID, inv.ID)
	}
	if res.AlreadyConsumedForInvoice {
		t.Fatalf("first run reported AlreadyConsumedForInvoice=true")
	}
	if res.ConsumedCents != 250 {
		t.Fatalf("ConsumedCents = %d, want 250", res.ConsumedCents)
	}
	if got := len(res.PerCredit); got != 2 {
		t.Fatalf("PerCredit len = %d, want 2", got)
	}
	// FIFO order check: first drained credit is the oldest.
	if res.PerCredit[0].CreditID != oldest.ID {
		t.Fatalf("PerCredit[0].CreditID = %s, want %s (oldest)", res.PerCredit[0].CreditID, oldest.ID)
	}
	if res.PerCredit[0].DeltaCents != -100 {
		t.Fatalf("PerCredit[0].DeltaCents = %d, want -100", res.PerCredit[0].DeltaCents)
	}
	if res.PerCredit[0].NewBalance != 0 {
		t.Fatalf("PerCredit[0].NewBalance = %d, want 0", res.PerCredit[0].NewBalance)
	}
	if res.PerCredit[1].CreditID != mid.ID {
		t.Fatalf("PerCredit[1].CreditID = %s, want %s (mid)", res.PerCredit[1].CreditID, mid.ID)
	}
	if res.PerCredit[1].DeltaCents != -150 {
		t.Fatalf("PerCredit[1].DeltaCents = %d, want -150", res.PerCredit[1].DeltaCents)
	}
	if res.PerCredit[1].NewBalance != 50 {
		t.Fatalf("PerCredit[1].NewBalance = %d, want 50", res.PerCredit[1].NewBalance)
	}
	// RemainingCreditsCents sums the active credits after the call:
	// mid 50 + newest 300 = 350.
	if res.RemainingCreditsCents != 350 {
		t.Fatalf("RemainingCreditsCents = %d, want 350", res.RemainingCreditsCents)
	}

	// Verify the persisted account_credits reflect the drain.
	credits, err := store.ListAccountCredits(ctx, acct.ID, false)
	if err != nil {
		t.Fatalf("list credits: %v", err)
	}
	for _, c := range credits {
		switch c.ID {
		case oldest.ID:
			if c.CentsRemaining != 0 {
				t.Fatalf("oldest cents_remaining = %d, want 0", c.CentsRemaining)
			}
		case mid.ID:
			if c.CentsRemaining != 50 {
				t.Fatalf("mid cents_remaining = %d, want 50", c.CentsRemaining)
			}
		case newest.ID:
			if c.CentsRemaining != 300 {
				t.Fatalf("newest cents_remaining = %d, want 300 (untouched)", c.CentsRemaining)
			}
		}
	}

	// Verify the ledger has exactly 2 consumption rows with the
	// provider_invoice_id stamped.
	ledger := store.ListCreditLedgerForTest(acct.ID)
	if got := len(ledger); got != 2 {
		t.Fatalf("ledger len = %d, want 2", got)
	}
	for _, le := range ledger {
		if le.ProviderInvoiceID == nil || *le.ProviderInvoiceID != inv.ProviderInvoiceID {
			t.Fatalf("ledger row %s missing provider_invoice_id", le.ID)
		}
		if le.DeltaCents >= 0 {
			t.Fatalf("ledger row %s has non-negative delta %d", le.ID, le.DeltaCents)
		}
	}

	// Idempotency — second call with the same provider_invoice_id
	// must be a no-op: same ConsumedCents, AlreadyConsumedForInvoice=true,
	// and the ledger row count must NOT grow.
	res2, _, err := ConsumeCreditsForInvoice(ctx, store, inv.ID, "apid", "replay")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !res2.AlreadyConsumedForInvoice {
		t.Fatalf("replay: AlreadyConsumedForInvoice = false, want true")
	}
	if res2.ConsumedCents != 250 {
		t.Fatalf("replay ConsumedCents = %d, want 250", res2.ConsumedCents)
	}
	ledger2 := store.ListCreditLedgerForTest(acct.ID)
	if len(ledger2) != 2 {
		t.Fatalf("replay ledger len = %d, want 2 (idempotent)", len(ledger2))
	}
}

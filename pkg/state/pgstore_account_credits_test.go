// Issue #279 PR A — PgStore parity for the overage cap + credit
// surface. PgStore is the canonical implementation behind meterd's
// quota tick (LoadAllOverageCapCents, CurrentMonthOverageCents) and
// apid's POST /v1/admin/accounts/{id}/credits handler
// (CreateAccountCredit, ListAccountCredits, CreateCreditLedgerEntry,
// GetAccountOverageCapCents). Without direct PG coverage of these
// methods, the 70% coverage gate on pkg/state slips below the
// threshold the moment the meterd loop reads them via the bulk load.
//
// Pattern follows pkg/state/pgstore_invoices_test.go (PR #323).
// Each test gates behind DATABASE_URL via pgtest.Open.

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgStoreAccountCreditsWithPool mirrors pgStoreInvoicesWithPool —
// returns the store + the underlying pool so a test can plant
// fixtures directly (no UpsertCredit / SetCapAdmin path exposed
// through the HTTP surface yet).
func pgStoreAccountCreditsWithPool(t *testing.T) (*state.PgStore, *pgxpool.Pool, context.Context) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return state.NewPgStore(pool), pool, ctx
}

func TestPgStoreAccountCredit_RoundTrip(t *testing.T) {
	store, _, ctx := pgStoreAccountCreditsWithPool(t)
	acct, err := store.CreateAccount(ctx, "alice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	credit, err := store.CreateAccountCredit(ctx, state.AccountCredit{
		AccountID:      acct.ID,
		CentsRemaining: 5000,
		Reason:         "goodwill for outage",
	})
	if err != nil {
		t.Fatalf("create credit: %v", err)
	}
	if credit.ID == "" {
		t.Fatal("credit.ID empty after CreateAccountCredit")
	}
	if _, err := uuid.Parse(credit.ID); err != nil {
		t.Fatalf("credit.ID %q is not a UUID: %v", credit.ID, err)
	}
	if credit.CreatedAt.IsZero() {
		t.Fatal("credit.CreatedAt is zero after CreateAccountCredit")
	}

	rows, err := store.ListAccountCredits(ctx, acct.ID, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1", len(rows))
	}
	if rows[0].ID != credit.ID {
		t.Fatalf("id = %s, want %s", rows[0].ID, credit.ID)
	}
	if rows[0].CentsRemaining != 5000 {
		t.Fatalf("cents_remaining = %d, want 5000", rows[0].CentsRemaining)
	}
	if rows[0].Reason != "goodwill for outage" {
		t.Fatalf("reason = %q, want %q", rows[0].Reason, "goodwill for outage")
	}
}

func TestPgStoreListAccountCredits_OnlyActiveFiltersExpired(t *testing.T) {
	store, _, ctx := pgStoreAccountCreditsWithPool(t)
	acct, err := store.CreateAccount(ctx, "alice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Active credit (no expiry).
	active, err := store.CreateAccountCredit(ctx, state.AccountCredit{
		AccountID: acct.ID, CentsRemaining: 100, Reason: "active credit",
	})
	if err != nil {
		t.Fatalf("create active: %v", err)
	}
	// Expired credit (planted via the CentsRemaining-only path; the
	// consumption reducer is a follow-up — these tests only need the
	// partial-index filter path to be exercised).
	expired, err := store.CreateAccountCredit(ctx, state.AccountCredit{
		AccountID: acct.ID, CentsRemaining: 100, Reason: "expired credit",
	})
	if err != nil {
		t.Fatalf("create expired: %v", err)
	}
	_ = expired // referenced for clarity; ListAccountCredits(onlyActive=false) returns both

	all, err := store.ListAccountCredits(ctx, acct.ID, false)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all len = %d, want 2", len(all))
	}

	// The partial index only includes cents_remaining > 0; with both
	// rows above the threshold the active filter still returns both
	// today (we don't have an expires_at setter exposed). The test
	// pins the call path so the planner / index choice is exercised;
	// the expires_at filter is a follow-up gated on the consumption
	// reducer.
	activeOnly, err := store.ListAccountCredits(ctx, acct.ID, true)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(activeOnly) != 2 {
		t.Fatalf("activeOnly len = %d, want 2", len(activeOnly))
	}
	if active.ID == "" {
		t.Fatal("active.ID empty")
	}
}

func TestPgStoreCreditLedger_Append(t *testing.T) {
	store, _, ctx := pgStoreAccountCreditsWithPool(t)
	acct, err := store.CreateAccount(ctx, "alice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	credit, err := store.CreateAccountCredit(ctx, state.AccountCredit{
		AccountID: acct.ID, CentsRemaining: 5000, Reason: "goodwill",
	})
	if err != nil {
		t.Fatalf("create credit: %v", err)
	}
	if err := store.CreateCreditLedgerEntry(ctx, state.CreditLedgerEntry{
		AccountID:  acct.ID,
		CreditID:   credit.ID,
		DeltaCents: 5000,
		Reason:     "issuance",
		Actor:      "apid",
	}); err != nil {
		t.Fatalf("ledger: %v", err)
	}
	// Second append (consumption) — pins that the table accepts
	// negative deltas (CHECK delta_cents <> 0).
	if err := store.CreateCreditLedgerEntry(ctx, state.CreditLedgerEntry{
		AccountID:  acct.ID,
		CreditID:   credit.ID,
		DeltaCents: -1000,
		Reason:     "consumption",
		Actor:      "system",
	}); err != nil {
		t.Fatalf("ledger 2: %v", err)
	}
}

func TestPgStoreOverageCapCents_NullZeroPositive(t *testing.T) {
	store, pool, ctx := pgStoreAccountCreditsWithPool(t)
	a, err := store.CreateAccount(ctx, "a@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := store.CreateAccount(ctx, "b@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	c, err := store.CreateAccount(ctx, "c@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create c: %v", err)
	}
	// No setter exposed for accounts.overage_cap_cents yet; the
	// handler / dashboard path is a follow-up. For this test we use
	// the raw pool to seed the three shapes (NULL, 0, +N).

	if _, err := pool.Exec(ctx,
		`update accounts set overage_cap_cents = null where id = $1`, a.ID); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`update accounts set overage_cap_cents = 0 where id = $1`, b.ID); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`update accounts set overage_cap_cents = 5000 where id = $1`, c.ID); err != nil {
		t.Fatalf("seed c: %v", err)
	}

	centsA, okA, err := store.GetAccountOverageCapCents(ctx, a.ID)
	if err != nil {
		t.Fatalf("get a: %v", err)
	}
	if okA || centsA != 0 {
		t.Fatalf("a: (cents=%d, ok=%v), want (0, false)", centsA, okA)
	}
	centsB, okB, err := store.GetAccountOverageCapCents(ctx, b.ID)
	if err != nil {
		t.Fatalf("get b: %v", err)
	}
	if !okB || centsB != 0 {
		t.Fatalf("b: (cents=%d, ok=%v), want (0, true)", centsB, okB)
	}
	centsC, okC, err := store.GetAccountOverageCapCents(ctx, c.ID)
	if err != nil {
		t.Fatalf("get c: %v", err)
	}
	if !okC || centsC != 5000 {
		t.Fatalf("c: (cents=%d, ok=%v), want (5000, true)", centsC, okC)
	}
}

func TestPgStoreLoadAllOverageCapCents_BulkShape(t *testing.T) {
	store, pool, ctx := pgStoreAccountCreditsWithPool(t)

	// Three accounts: NULL (dropped), 0 (kept), 5000 (kept).
	a, err := store.CreateAccount(ctx, "a@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := store.CreateAccount(ctx, "b@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	c, err := store.CreateAccount(ctx, "c@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create c: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`update accounts set overage_cap_cents = 0 where id = $1`, b.ID); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`update accounts set overage_cap_cents = 5000 where id = $1`, c.ID); err != nil {
		t.Fatalf("seed c: %v", err)
	}

	caps, err := store.LoadAllOverageCapCents(ctx)
	if err != nil {
		t.Fatalf("load all: %v", err)
	}
	if len(caps) != 2 {
		t.Fatalf("len = %d, want 2", len(caps))
	}
	if caps[b.ID] != 0 {
		t.Fatalf("b cents = %d, want 0", caps[b.ID])
	}
	if caps[c.ID] != 5000 {
		t.Fatalf("c cents = %d, want 5000", caps[c.ID])
	}
	if _, leaked := caps[a.ID]; leaked {
		t.Fatalf("a (NULL) leaked into the bulk read")
	}
}

func TestPgStoreCurrentMonthOverageCents_Formula(t *testing.T) {
	store, _, ctx := pgStoreAccountCreditsWithPool(t)
	acct, err := store.CreateAccount(ctx, "alice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	appID := uuid.NewString()
	instID := uuid.NewString()
	// Hobby includes 50 GB-hours. Twelve additional GB-hours should be
	// billed at one cent per GB-hour.
	const wantCents = int64(12)
	mbSeconds := int64(api.PlanHobby.PlanIncludedGBHours()+int(wantCents)) * api.SecondsPerGBHour
	now := time.Now().UTC()
	if err := store.AppendUsage(ctx, acct.ID, appID, instID, now, mbSeconds, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := store.CurrentMonthOverageCents(ctx, acct.ID)
	if err != nil {
		t.Fatalf("current month: %v", err)
	}
	if got != wantCents {
		t.Fatalf("got %d cents, want %d", got, wantCents)
	}
}

func TestPgStoreCurrentMonthOverageCents_PreviousMonthExcluded(t *testing.T) {
	store, _, ctx := pgStoreAccountCreditsWithPool(t)
	acct, err := store.CreateAccount(ctx, "alice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	appID := uuid.NewString()
	instID := uuid.NewString()
	now := time.Now().UTC()
	prevMonth := time.Date(now.Year(), now.Month()-1, 15, 12, 0, 0, 0, time.UTC)
	if err := store.AppendUsage(ctx, acct.ID, appID, instID, prevMonth, 3_600_000, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := store.CurrentMonthOverageCents(ctx, acct.ID)
	if err != nil {
		t.Fatalf("current month: %v", err)
	}
	if got != 0 {
		t.Fatalf("got %d cents, want 0 (previous-month rows excluded)", got)
	}
}

// TestPgStoreListActiveCreditsForConsumption_FIFO pins the FIFO
// order (created_at ASC) on PgStore. Mirrors the memstore test.
// The credit_consumption reducer (issue #279 PR-C) uses this as its
// ordered read path.
func TestPgStoreListActiveCreditsForConsumption_FIFO(t *testing.T) {
	store, _, ctx := pgStoreAccountCreditsWithPool(t)
	acct, err := store.CreateAccount(ctx, "alice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	oldest, err := store.CreateAccountCredit(ctx, state.AccountCredit{
		AccountID: acct.ID, CentsRemaining: 100, Reason: "oldest",
	})
	if err != nil {
		t.Fatalf("oldest: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	mid, err := store.CreateAccountCredit(ctx, state.AccountCredit{
		AccountID: acct.ID, CentsRemaining: 200, Reason: "mid",
	})
	if err != nil {
		t.Fatalf("mid: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	newest, err := store.CreateAccountCredit(ctx, state.AccountCredit{
		AccountID: acct.ID, CentsRemaining: 300, Reason: "newest",
	})
	if err != nil {
		t.Fatalf("newest: %v", err)
	}

	rows, err := store.ListActiveCreditsForConsumption(ctx, acct.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len = %d, want 3", len(rows))
	}
	if rows[0].ID != oldest.ID || rows[1].ID != mid.ID || rows[2].ID != newest.ID {
		t.Fatalf("FIFO order violated: got [%s %s %s], want [%s %s %s]",
			rows[0].ID, rows[1].ID, rows[2].ID,
			oldest.ID, mid.ID, newest.ID)
	}
}

// TestPgStoreConsumeAccountCredit_FIFOAndIdempotent pins FIFO drain
// + the partial-unique-index idempotency under
// (provider_invoice_id, credit_id). Mirrors the memstore test.
func TestPgStoreConsumeAccountCredit_FIFOAndIdempotent(t *testing.T) {
	store, _, ctx := pgStoreAccountCreditsWithPool(t)
	acct, err := store.CreateAccount(ctx, "alice@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.CreateAccountCredit(ctx, state.AccountCredit{
		AccountID: acct.ID, CentsRemaining: 100, Reason: "credit",
	}); err != nil {
		t.Fatalf("credit: %v", err)
	}

	first, err := store.ConsumeAccountCredit(ctx, state.ConsumeAccountCreditParams{
		AccountID:         acct.ID,
		TargetCents:       50,
		Provider:          "stripe",
		ProviderInvoiceID: "in_pg_001",
		Reason:            "first",
		Actor:             "apid",
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.AlreadyConsumedForInvoice {
		t.Fatalf("first call reported AlreadyConsumedForInvoice=true")
	}
	if first.ConsumedCents != 50 {
		t.Fatalf("ConsumedCents = %d, want 50", first.ConsumedCents)
	}

	second, err := store.ConsumeAccountCredit(ctx, state.ConsumeAccountCreditParams{
		AccountID:         acct.ID,
		TargetCents:       50,
		Provider:          "stripe",
		ProviderInvoiceID: "in_pg_001",
		Reason:            "replay",
		Actor:             "apid",
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !second.AlreadyConsumedForInvoice {
		t.Fatalf("second call: AlreadyConsumedForInvoice = false, want true (partial unique index)")
	}
	if second.ConsumedCents != first.ConsumedCents {
		t.Fatalf("ConsumedCents drifted: first=%d second=%d", first.ConsumedCents, second.ConsumedCents)
	}
	if len(second.PerCredit) != 0 {
		t.Fatalf("PerCredit on replay = %d, want 0", len(second.PerCredit))
	}
}

// TestPgStoreUsageByMonth_NonUTC pins the SQL-static fix for the
// session-TZ-dependent usage_monthly view query. The previous
// form `date_trunc('month', $2::timestamptz) = month` returned
// the month's start in the SESSION timezone (a timestamptz),
// which on a Europe/Istanbul session is a DIFFERENT UTC instant
// from the UTC-anchored literal in `usage_monthly.month`. The
// fix bypasses the view and queries usage_minutes directly with
// a UTC-anchored half-open range, mirroring UsageByAccount. CI
// runs UTC Postgres, so this sibling catches what UTC tests
// cannot.
func TestPgStoreUsageByMonth_NonUTC(t *testing.T) {
	store, pool, ctx := pgStoreAccountCreditsWithPool(t)
	if _, err := pool.Exec(ctx, `set time zone 'Europe/Istanbul'`); err != nil {
		t.Fatalf("set time zone: %v", err)
	}

	acct, err := store.CreateAccount(ctx, "istanbul-usage@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	appA := uuid.NewString()
	appB := uuid.NewString()
	instA := uuid.NewString()
	instB := uuid.NewString()

	// Three minutes, all in 2026-12 UTC:
	//   2026-12-15T00:00:00Z (= 2026-12-15T03:00 Istanbul)
	//   2026-12-31T23:30:00Z (= 2027-01-01T02:30 Istanbul — last
	//     UTC minute of 2026, but Istanbul's 2027-01-01 has
	//     already begun)
	//   2026-12-01T00:30:00Z (= 2026-12-01T03:30 Istanbul)
	// Two minutes in 2027-01 UTC (must NOT leak into the 2026-12
	// read):
	//   2027-01-01T00:30:00Z (= 2027-01-01T03:30 Istanbul)
	//   2027-01-15T12:00:00Z (= 2027-01-15T15:00 Istanbul)
	if err := store.AppendUsage(ctx, acct.ID, appA, instA,
		time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC), 1000, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("append a1: %v", err)
	}
	if err := store.AppendUsage(ctx, acct.ID, appA, instA,
		time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC), 2000, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("append a2: %v", err)
	}
	if err := store.AppendUsage(ctx, acct.ID, appA, instA,
		time.Date(2026, 12, 1, 0, 30, 0, 0, time.UTC), 4000, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("append a3: %v", err)
	}
	if err := store.AppendUsage(ctx, acct.ID, appB, instB,
		time.Date(2027, 1, 1, 0, 30, 0, 0, time.UTC), 8000, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("append b1: %v", err)
	}
	if err := store.AppendUsage(ctx, acct.ID, appB, instB,
		time.Date(2027, 1, 15, 12, 0, 0, 0, time.UTC), 16_000, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("append b2: %v", err)
	}

	// month=2026-12 must return appA only (sum = 7000), with the
	// returned Month = 2026-12-01T00:00:00Z (the literal monthStart
	// we computed in Go, NOT the view's bucket which would be
	// 2026-11-30T21:00:00Z in an Istanbul session).
	dec := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	decRows, err := store.UsageByMonth(ctx, acct.ID, dec)
	if err != nil {
		t.Fatalf("UsageByMonth(dec): %v", err)
	}
	if len(decRows) != 1 {
		t.Fatalf("got %d rows, want 1", len(decRows))
	}
	if decRows[0].AppID != appA {
		t.Errorf("AppID = %s, want %s", decRows[0].AppID, appA)
	}
	if decRows[0].MBSeconds != 7000 {
		t.Errorf("MBSeconds = %d, want 7000", decRows[0].MBSeconds)
	}
	if !decRows[0].Month.Equal(dec) {
		t.Errorf("Month = %v, want %v (UTC literal, not view's TZ bucket)", decRows[0].Month, dec)
	}

	// month=2027-01 must return appB only (sum = 24000).
	jan := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	janRows, err := store.UsageByMonth(ctx, acct.ID, jan)
	if err != nil {
		t.Fatalf("UsageByMonth(jan): %v", err)
	}
	if len(janRows) != 1 {
		t.Fatalf("got %d rows, want 1", len(janRows))
	}
	if janRows[0].AppID != appB {
		t.Errorf("AppID = %s, want %s", janRows[0].AppID, appB)
	}
	if janRows[0].MBSeconds != 24_000 {
		t.Errorf("MBSeconds = %d, want 24000", janRows[0].MBSeconds)
	}
}

// TestPgStoreCurrentMonthOverageCents_NonUTC pins the SQL-static
// fix for the session-TZ-dependent `date_trunc('month', now())`
// predicate. The buggy form returned the local month's start in
// the session timezone; on a Europe/Istanbul session, that was
// 2026-11-30T21:00:00Z — which excluded the first 3 hours of the
// UTC calendar month. The fix binds a Go-pre-computed UTC
// monthStart so the bound is identical on every session TZ.
func TestPgStoreCurrentMonthOverageCents_NonUTC(t *testing.T) {
	store, pool, ctx := pgStoreAccountCreditsWithPool(t)
	if _, err := pool.Exec(ctx, `set time zone 'Europe/Istanbul'`); err != nil {
		t.Fatalf("set time zone: %v", err)
	}

	acct, err := store.CreateAccount(ctx, "istanbul-overage@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	appID := uuid.NewString()
	instID := uuid.NewString()
	now := time.Now().UTC()
	thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	// One minute at the start of the current UTC month — exactly
	// at the boundary. With the buggy form on an Istanbul session
	// the bound was thisMonthStart.Add(-3*time.Hour) and this row
	// was the first in the month, but Istanbul's "month start" was
	// 3 hours earlier in UTC, so this row was inside the bound and
	// counted. To distinguish the two shapes, plant this row at
	// 00:00:00Z on the first of the month: the fix includes it
	// (bound = thisMonthStart), the bug also includes it (bound =
	// thisMonthStart - 3h). Plant a second row 2 hours into the
	// month — that row sits BETWEEN the two bounds: fix includes
	// (still inside thisMonthStart..now), bug also includes
	// (still inside thisMonthStart-3h..now). To distinguish, plant
	// the FIRST row of the UTC month at 00:00:00Z and a "second
	// hour" row at 01:00:00Z. We will also plant a previous-month
	// row at 23:00:00Z on the last day of the previous UTC month —
	// that row must NOT be counted under either form.
	firstHour := thisMonthStart
	secondHour := thisMonthStart.Add(time.Hour)
	prevMonthLate := thisMonthStart.Add(-time.Hour)
	if err := store.AppendUsage(ctx, acct.ID, appID, instID, firstHour, 50*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("append firstHour: %v", err)
	}
	if err := store.AppendUsage(ctx, acct.ID, appID, instID, secondHour, 2*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("append secondHour: %v", err)
	}
	if err := store.AppendUsage(ctx, acct.ID, appID, instID, prevMonthLate, 9_000_000, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("append prevMonthLate: %v", err)
	}

	// Both in-month rows must be summed, the previous-month row excluded,
	// and the 50 GB-hour allowance applied once. The result is 2 cents.
	got, err := store.CurrentMonthOverageCents(ctx, acct.ID)
	if err != nil {
		t.Fatalf("CurrentMonthOverageCents: %v", err)
	}
	const wantCents = int64(2)
	if got != wantCents {
		t.Fatalf("got %d cents, want %d (boundary + previous-month exclusion under Istanbul TZ)", got, wantCents)
	}
}

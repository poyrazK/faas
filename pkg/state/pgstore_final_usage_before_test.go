package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// Usage before an hour is final once meterd can no longer write a minute
// before it (ADR-1162): api.MeterUsageFinalAfter after the hour.
func TestUsageFinalBefore(t *testing.T) {
	at := time.Date(2026, 11, 8, 13, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		now  time.Time
		want bool
	}{
		{at.Add(time.Hour), false},
		{at.Add(api.MeterUsageFinalAfter - time.Second), false},
		{at.Add(api.MeterUsageFinalAfter), true},
		{at.Add(11 * 24 * time.Hour), true},
	} {
		if got := state.UsageFinalBefore(at, c.now); got != c.want {
			t.Errorf("UsageFinalBefore(%s, %s) = %t, want %t", at, c.now, got, c.want)
		}
	}
}

// A final usage sum is the minute rows in [month start, at), summed once per
// process: a later read returns the kept sum, per account, and a new month
// starts over.
func TestPgStoreFinalUsageBeforeSumsOnceAndKeeps(t *testing.T) {
	store, pool, ctx := pgStoreAccountCreditsWithPool(t)
	monthStart := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	at := monthStart.Add(3 * time.Hour)
	var accounts []string
	for _, email := range []string{"final-a@example.com", "final-b@example.com"} {
		acct, err := store.CreateAccount(ctx, email, api.PlanScale)
		if err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, acct.ID)
	}
	for i, row := range []struct {
		account int
		minute  time.Time
		mb      int64
	}{
		{0, monthStart.Add(-time.Minute), 1}, // last month
		{0, monthStart, 100},                 // first minute counts
		{0, at.Add(-time.Minute), 200},       // last minute before at counts
		{0, at, 400},                         // at itself does not
		{1, monthStart.Add(time.Hour), 7},    // another account's own sum
	} {
		if err := store.AppendUsage(ctx, accounts[row.account], uuid.NewString(), uuid.NewString(), row.minute, row.mb, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatalf("append row %d: %v", i, err)
		}
	}
	read := func(account int, month, before time.Time) int64 {
		t.Helper()
		sum, err := store.FinalUsageBeforeForTest(ctx, accounts[account], month, before)
		if err != nil {
			t.Fatal(err)
		}
		return sum
	}
	if got := read(0, monthStart, at); got != 300 {
		t.Fatalf("final usage before %s = %d, want 300", at, got)
	}
	if got := read(1, monthStart, at); got != 7 {
		t.Fatalf("other account's final usage = %d, want 7", got)
	}
	// Final rows never change; if one did, the kept sum shows it was not
	// summed again.
	if _, err := pool.Exec(ctx, `update usage_minutes set mb_seconds = mb_seconds * 10 where account_id = $1`, accounts[0]); err != nil {
		t.Fatal(err)
	}
	if got := read(0, monthStart, at); got != 300 {
		t.Fatalf("second read = %d, want the kept 300", got)
	}
	// Another month is summed afresh, which drops the previous month's sums.
	if got := read(0, monthStart.AddDate(0, -1, 0), monthStart); got != 10 {
		t.Fatalf("previous month's final usage = %d, want 10", got)
	}
	if got := read(0, monthStart, at); got != 3000 {
		t.Fatalf("read after a month change = %d, want 3000 summed afresh", got)
	}
}

// An account whose plan changed this month is billed in segments split at the
// change's hour; the spend cap's figure matches MonthBilledComputeMBSeconds
// with the usage before that hour, whether that usage is already final (read
// once and kept) or not.
// adr: 20261031100000
func TestPgStoreCurrentMonthOverageCents_PlanChangedThisMonth(t *testing.T) {
	store, pool, ctx := pgStoreAccountCreditsWithPool(t)
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	change := monthStart.Add(time.Hour + 30*time.Minute)
	if now.Before(change.Add(time.Hour)) {
		t.Skip("needs an hour of the month after the plan change")
	}
	acct, err := store.CreateAccount(ctx, "segments@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	var snapshots []state.FinancialPriceSnapshot
	for _, g := range []struct {
		plan api.Plan
		at   time.Time
	}{{api.PlanPro, monthStart}, {api.PlanHobby, change}} {
		s, err := store.PutFinancialPriceSnapshot(ctx, state.FinancialPriceSnapshot{
			AccountID: acct.ID, PeriodStart: monthStart, PeriodEnd: monthStart.AddDate(0, 1, 0), Plan: g.plan, EffectiveFrom: g.at,
			DeliveryMode: "live",
			Price: financial.Price{Version: "test-" + string(g.plan), Meter: "compute", Currency: "USD", Unit: "mb_seconds",
				UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: api.IncludedMBSeconds(g.plan)},
		})
		if err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, s)
	}
	if err := store.UpdateAccountPlan(ctx, acct.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	gbh := api.SecondsPerGBHour
	before, after := 300*gbh, 7*gbh
	beforeInstance := uuid.NewString()
	if err := store.AppendUsage(ctx, acct.ID, uuid.NewString(), beforeInstance, monthStart.Add(time.Minute), before, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, acct.ID, uuid.NewString(), uuid.NewString(), change.Add(time.Hour), after, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	want := func(total, usageBefore int64) int64 {
		t.Helper()
		billed, err := state.CurrentMonthBilledComputeMBSeconds(api.PlanHobby, snapshots, time.Now().UTC(), total,
			func(time.Time) (int64, error) { return usageBefore, nil })
		if err != nil {
			t.Fatal(err)
		}
		return api.OverageCentsForBillableMBSeconds(billed)
	}
	got, err := store.CurrentMonthOverageCents(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w := want(before+after, before); got != w || w == 0 {
		t.Fatalf("overage = %d cents, want %d (non-zero)", got, w)
	}
	if !state.UsageFinalBefore(change.Truncate(time.Hour), now) {
		return
	}
	// The usage before the change's hour was final and is kept: a row that
	// changed anyway moves only the month's total.
	if _, err := pool.Exec(ctx, `update usage_minutes set mb_seconds = mb_seconds * 2 where instance_id = $1`, beforeInstance); err != nil {
		t.Fatal(err)
	}
	got, err = store.CurrentMonthOverageCents(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w := want(2*before+after, before); got != w {
		t.Fatalf("overage after the final usage changed = %d cents, want %d from the kept usage", got, w)
	}
}

// adr: 20261019193600
//
// Ledger dual run: the ledger's net compute against the pusher's receipts.
package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// ledgerRetention reports every month as retained, so the ledger's coverage
// is complete for the test months.
type ledgerRetention struct {
	MemStore *state.MemStore
}

func (ledgerRetention) FinancialEvidenceCoverage(context.Context) (time.Time, error) {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), nil
}

// putSmallComputeGrant records a 100 MB-second allowance so a few hundred
// MB-seconds of usage produce overage on both sides.
func putSmallComputeGrant(t *testing.T, ctx context.Context, s *state.MemStore, acct state.Account, monthStart time.Time) {
	t.Helper()
	if _, err := s.PutFinancialPriceSnapshot(ctx, state.FinancialPriceSnapshot{
		AccountID: acct.ID, PeriodStart: monthStart, PeriodEnd: monthStart.AddDate(0, 1, 0),
		Price: financial.Price{Version: "small-" + acct.ID, Meter: "compute", Currency: "USD", Unit: "mb_seconds",
			UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: 100},
		Plan: acct.Plan, EffectiveFrom: monthStart, DeliveryMode: "live",
	}); err != nil {
		t.Fatal(err)
	}
}

// makeStripeAccount is a paid Hobby account with a stripe billing identity
// that has billed since 1970, so every test hour is inside its window.
func makeStripeAccount(t *testing.T, ctx context.Context, s *state.MemStore, email string) state.Account {
	t.Helper()
	acct, err := s.CreateAccount(ctx, email, api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBillingIdentity(ctx, state.BillingIdentity{
		AccountID: acct.ID, Provider: "stripe", CustomerID: "cus-" + acct.ID, SubscriptionID: "sub-" + acct.ID,
		BillingFrom: time.Unix(0, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccountProviderCustomerID(ctx, acct.ID, "cus-"+acct.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, "sub-"+acct.ID); err != nil {
		t.Fatal(err)
	}
	acct, err = s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	return acct
}

func TestLedgerReconcilerClassifiesAccounts(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	store := statetest.NewMem(ledgerRetention{MemStore: mem})
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	hour := month.Add(10*24*time.Hour + 10*time.Hour)
	now := hour.Add(3 * time.Hour)

	accounts := map[string]state.Account{}
	for _, name := range []string{"match", "drift", "pending", "capped"} {
		acct := makeStripeAccount(t, ctx, mem, name+"@ledger.example")
		putSmallComputeGrant(t, ctx, mem, acct, month)
		if err := mem.AppendUsage(ctx, acct.ID, "app-"+name, "instance-"+name, hour.Add(5*time.Minute), 300, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		accounts[name] = acct
	}
	mem.SetOverageCapCentsForTest(accounts["capped"].ID, 0)
	completeUsageThrough(t, ctx, mem, now)

	// Polar and Paddle deliver overage above the allowance.
	provider := &recordingOverage{}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	if _, err := pusher.PushPending(ctx, 31*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	// A receipt with no usage behind it, and usage the pusher has not reached.
	if err := mem.RecordBillingUsageDelivery(ctx, "stripe", accounts["drift"].ID, hour.Add(time.Hour), 50); err != nil {
		t.Fatal(err)
	}
	if err := mem.AppendUsage(ctx, accounts["pending"].ID, "app-pending", "instance-pending", hour.Add(time.Hour+5*time.Minute), 300, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	reg := prometheus.NewRegistry()
	reconciler := meter.NewLedgerReconciler(pusher, "stripe", reg)
	passes, err := reconciler.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(passes) != 2 || passes[0].Month != "previous" || passes[1].Month != "current" {
		t.Fatalf("passes = %+v, want previous then current", passes)
	}
	current := passes[1]
	want := map[string]int{meter.LedgerMatch: 1, meter.LedgerDrift: 1, meter.LedgerPending: 1, meter.LedgerCapped: 1}
	for result, n := range want {
		if current.Results[result] != n {
			t.Fatalf("current month results = %v, want %v", current.Results, want)
		}
	}
	if current.DriftMBSeconds != 50 {
		t.Fatalf("drift = %d MB-seconds, want 50", current.DriftMBSeconds)
	}
	// August has no price contract or sampling coverage: the ledger cannot
	// vouch for it, so nothing is claimed to match.
	if previous := passes[0]; previous.Results[meter.LedgerIncomplete] != 4 || len(previous.Results) != 1 {
		t.Fatalf("previous month results = %v, want 4 incomplete", previous.Results)
	}
	if got := testutil.ToFloat64(reconciler.AccountsGauge().WithLabelValues("current", meter.LedgerDrift)); got != 1 {
		t.Fatalf("drift gauge = %v, want 1", got)
	}
}

// Legacy Stripe receives all usage and applies the allowance itself, so the
// dual run compares against the ledger's gross quantity.
func TestLedgerReconcilerRawModeComparesGross(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	store := statetest.NewMem(ledgerRetention{MemStore: mem})
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	hour := month.Add(10 * time.Hour)
	now := hour.Add(3 * time.Hour)
	acct := makeStripeAccount(t, ctx, mem, "raw@ledger.example")
	putSmallComputeGrant(t, ctx, mem, acct, month)
	if err := mem.AppendUsage(ctx, acct.ID, "app", "instance", hour, 300, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	completeUsageThrough(t, ctx, mem, now)
	pusher := meter.NewPusher(store, &recordingStripe{}, discardLog(), func() time.Time { return now }, nil)
	if _, err := pusher.PushPending(ctx, 31*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	passes, err := meter.NewLedgerReconciler(pusher, "stripe", nil).RunOnce(ctx)
	if err != nil || len(passes) != 2 || passes[1].Results[meter.LedgerMatch] != 1 {
		t.Fatalf("raw-mode dual run = %+v, %v; want one current-month match", passes, err)
	}
}

func TestLedgerReconcilerDisabledWithoutProvider(t *testing.T) {
	mem := state.NewMemStore()
	pusher := meter.NewPusher(mem, &recordingStripe{}, discardLog(), time.Now, nil)
	passes, err := meter.NewLedgerReconciler(pusher, "", nil).RunOnce(context.Background())
	if err != nil || passes != nil {
		t.Fatalf("RunOnce without a provider = (%+v, %v), want nothing", passes, err)
	}
}

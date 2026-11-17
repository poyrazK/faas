package billing

// Guards on the money paths Gregale bills its own customers through: the
// overage spend cap, credit consumption, currency conversion, financial
// quantity display, idempotency keys and managed PostgreSQL line items. Each
// case pins a branch the weekly mutation run (make test-mutation-billing)
// showed no test noticed changing.

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// An egress reading without a price reserves nothing; an egress price with a
// zero unit or a zero rate is invalid and fails the cap closed (ADR-1162).
func TestCapRemainingMillicentsEgressPolicyGuards(t *testing.T) {
	const capCents = 10
	wantFull := new(big.Rat).SetInt64(capCents * api.MillicentsPerCent)
	cases := []struct {
		name   string
		policy *MeterUsagePolicy
		egress int64
		wantOK bool
	}{
		{name: "no egress price reserves nothing", policy: nil, egress: 1_000, wantOK: true},
		{name: "zero unit quantity is invalid", policy: &MeterUsagePolicy{UnitQuantity: 0, MillicentsPerUnit: 5}, egress: 10},
		{name: "zero rate is invalid", policy: &MeterUsagePolicy{UnitQuantity: 1, MillicentsPerUnit: 0}, egress: 10},
		{name: "negative unit quantity is invalid", policy: &MeterUsagePolicy{UnitQuantity: -1, MillicentsPerUnit: 5}, egress: 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			remaining, ok := CapRemainingMillicents(capCents, 0, tc.egress, tc.policy, 0)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if tc.wantOK && remaining.Cmp(wantFull) != 0 {
				t.Fatalf("remaining = %s millicents, want the full %s", remaining.FloatString(0), wantFull.FloatString(0))
			}
		})
	}
}

// An invoice with only one end of its period is malformed: it neither bills
// all usage (the legacy no-period meaning) nor a half-open range.
func TestComputeInvoiceOverageCentsRejectsHalfPeriod(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "half-period@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	inv := state.Invoice{
		ID: uuid.NewString(), AccountID: acct.ID, Provider: "stripe", ProviderInvoiceID: "in_half",
		PeriodEnd: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
	if got, err := ComputeInvoiceOverageCents(ctx, store, inv); err == nil {
		t.Fatalf("half-period invoice = %d cents, want an error", got)
	}
}

// A zero cap (nothing paid left to refund) consumes no credit, even when the
// invoice's overage is positive.
func TestConsumeCreditsForInvoiceUpToZeroCapConsumesNothing(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "zero-cap@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAccountCredit(ctx, state.AccountCredit{AccountID: acct.ID, CentsRemaining: 500, Reason: "goodwill"}); err != nil {
		t.Fatal(err)
	}
	periodStart := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	inv := state.Invoice{
		ID: uuid.NewString(), AccountID: acct.ID, Provider: "stripe", ProviderInvoiceID: "in_zero_cap",
		PeriodStart: periodStart, PeriodEnd: periodStart.AddDate(0, 1, 0),
	}
	store.SeedInvoiceForTest(inv)
	// 50 billable GB-hours above Hobby's 200: 250 cents of overage.
	if err := store.AppendUsage(ctx, acct.ID, "app-1", "inst-1", periodStart.Add(time.Hour), 250*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	res, _, err := ConsumeCreditsForInvoiceUpTo(ctx, store, inv.ID, 0, "test", "zero cap")
	if err != nil {
		t.Fatal(err)
	}
	if res.ConsumedCents != 0 || res.RemainingCreditsCents != 500 {
		t.Fatalf("consumed %d, remaining %d; want 0 and 500", res.ConsumedCents, res.RemainingCreditsCents)
	}
}

// A provider currency with more decimals than ISO (Stripe's three-decimal
// currencies) divides down to the ISO minor unit, rounding half away.
func TestToISOMinorFromFinerProviderUnit(t *testing.T) {
	unit := MinorUnit{Exponent: 3, Step: 10}
	for amount, want := range map[int64]int64{1234: 123, 1235: 124, -1235: -124} {
		if got := unit.ToISOMinor(amount, 2); got != want {
			t.Fatalf("ToISOMinor(%d, 2) = %d, want %d", amount, got, want)
		}
	}
}

// Exactly 1024 bytes is one KiB.
func TestFormatInterfaceBytesKiBBoundary(t *testing.T) {
	if got := FormatFinancialQuantity(1024, "interface_bytes"); got != "1.00 KiB" {
		t.Fatalf("1024 bytes = %q, want 1.00 KiB", got)
	}
	if got := FormatFinancialQuantity(1023, "interface_bytes"); got != "1023 bytes" {
		t.Fatalf("1023 bytes = %q, want 1023 bytes", got)
	}
	// TiB is the largest unit: a PiB reads as 1024 TiB.
	if got := FormatFinancialQuantity(1<<50, "interface_bytes"); got != "1024.00 TiB" {
		t.Fatalf("1 PiB = %q, want 1024.00 TiB", got)
	}
}

// A nil parent still carries the key.
func TestContextWithIdempotencyKeyFromNilParent(t *testing.T) {
	var parent context.Context
	key, ok := IdempotencyKeyFromContext(ContextWithIdempotencyKey(parent, "key-1"))
	if !ok || key != "key-1" {
		t.Fatalf("key = %q, %v; want key-1", key, ok)
	}
}

// Line items need an account; a valid period alone is not enough.
func TestManagedPostgresUsageLineItemsRequireAccount(t *testing.T) {
	policy := managedpostgres.UsagePolicy{ComputeUnitHourMillicents: 3600}
	snapshot := managedpostgres.UsageSnapshot{PeriodStart: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), ComputeUnitSeconds: 3600}
	if _, err := ManagedPostgresUsageLineItems("", snapshot, policy); err == nil {
		t.Fatal("line items without an account were accepted")
	}
}

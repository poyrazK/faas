package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261109153000 — the spend alert reads what the month is billed,
// egress included, not the compute overage alone.
func TestMonthToDateSpendCountsLiveEgress(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	// The plan's whole compute allowance (no compute overage) and 2 GiB of
	// egress, two cents past Hobby's allowance in the test provider.
	if err := store.AppendUsage(ctx, acct.ID, "app", "instance", monthStart, api.IncludedMBSeconds(api.PlanHobby), 0, 0, 0, 2<<30, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	electEgressOverage(t, ctx, store, acct.ID)
	completeUsageThrough(t, ctx, store, now)

	compute, err := store.CurrentMonthOverageCents(ctx, acct.ID)
	if err != nil || compute != 0 {
		t.Fatalf("compute overage = %d, %v; want 0", compute, err)
	}
	provider := &combinedOverageProvider{recordingEgressProvider: recordingEgressProvider{mode: billing.MeterDeliveryLive}}
	got, err := meter.MonthToDateSpendCents(ctx, store, provider, acct, now)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("month spend = %d cents, want the 2 cents of egress", got)
	}
	if got, err := meter.MonthToDateSpendCents(ctx, store, nil, acct, now); err != nil || got != 0 {
		t.Fatalf("month spend without a provider = %d, %v; want 0", got, err)
	}
}

package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// A paid month warns exactly above the allowance billing removes for its
// recorded plans, after a downgrade (above the current plan's) and after an
// upgrade (below it); Free keeps its own allowance after a downgrade.
// adr: 20261030120000
func TestEnforceQuotaFollowsTheMonthsPlanHistory(t *testing.T) {
	ctx := context.Background()
	monthStart := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	change := monthStart.Add(15 * 24 * time.Hour)
	now := change.Add(time.Hour)

	for _, tc := range []struct {
		name     string
		from, to api.Plan
	}{
		{name: "pro to hobby", from: api.PlanPro, to: api.PlanHobby},
		{name: "hobby to pro", from: api.PlanHobby, to: api.PlanPro},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := state.NewMemStore()
			acct := makeBillableAccount(t, ctx, s, tc.from)
			putComputeGrant(t, ctx, s, acct, tc.from, monthStart)
			putComputeGrant(t, ctx, s, acct, tc.to, change)
			if err := s.UpdateAccountPlan(ctx, acct.ID, tc.to); err != nil {
				t.Fatal(err)
			}
			acct.Plan = tc.to
			snapshots, err := s.ListFinancialPriceSnapshots(ctx, acct.ID, monthStart)
			if err != nil {
				t.Fatal(err)
			}
			billed := int(state.MonthComputeIncludedMBSeconds(tc.to, snapshots, now) / api.SecondsPerGBHour)
			if billed == tc.to.PlanIncludedGBHours() {
				t.Fatalf("the month's allowance %d GB-h equals the current plan's; the case proves nothing", billed)
			}
			below, err := meter.EnforceQuota(ctx, s, &fakeNotifier{}, &fakeParker{}, &fakeMailer{}, discardLog(), acct, float64(billed-1), now)
			if err != nil || below.Action != "" {
				t.Fatalf("1 GB-h under the billed allowance %d: action %q, err %v; want no warning", billed, below.Action, err)
			}
			above, err := meter.EnforceQuota(ctx, s, &fakeNotifier{}, &fakeParker{}, &fakeMailer{}, discardLog(), acct, float64(billed+1), now)
			if err != nil || above.Action != "warn" || above.QuotaGB != billed {
				t.Fatalf("1 GB-h over the billed allowance %d: action %q quota %d, err %v; want a warning at %d", billed, above.Action, above.QuotaGB, err, billed)
			}
		})
	}

	// Free stops at its own allowance whatever plan the month began on.
	s := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, s, api.PlanPro)
	putComputeGrant(t, ctx, s, acct, api.PlanPro, monthStart)
	putComputeGrant(t, ctx, s, acct, api.PlanFree, change)
	if err := s.UpdateAccountPlan(ctx, acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	acct.Plan = api.PlanFree
	act, err := meter.EnforceQuota(ctx, s, &fakeNotifier{}, &fakeParker{}, &fakeMailer{}, discardLog(), acct,
		float64(api.PlanFree.PlanIncludedGBHours()+1), now)
	if err != nil || act.Action != "stop" {
		t.Fatalf("Free over its own allowance after a downgrade: action %q, err %v; want stop", act.Action, err)
	}
}

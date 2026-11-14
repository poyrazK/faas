// adr: 20261103131742 — amendment 2: managed PostgreSQL overage is billed
// span by span against the allowance in force, so a plan change never
// re-prices usage from before it.
package managedpostgres

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// usageBefore answers UsageBefore from the month's usage at each span end.
type usageBefore map[time.Time]int64

func (u usageBefore) UsageBefore(_ context.Context, _ string, month, before time.Time) (UsageSnapshot, error) {
	return UsageSnapshot{PeriodStart: monthStart(month), ComputeUnitSeconds: u[before]}, nil
}

func TestChargeBySpan(t *testing.T) {
	const hour = int64(3600)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	change, at := month.Add(15*24*time.Hour), month.Add(20*24*time.Hour)
	tests := []struct {
		name          string
		before, after int64 // allowance in CU-h before and after the change
		atChange      int64 // CU-h used before the change
		total         int64 // CU-h used through at
		wantBilled    int64 // CU-h
	}{
		// Pro → Hobby: the 90 CU-h Pro covered stay covered; the 10 used on
		// Hobby are above its 25, so they are billed. A current-plan rule
		// would bill Hobby's whole 25-hour room.
		{"downgrade bills only usage after it", 100, 25, 90, 100, 10},
		// Hobby → Pro: the 15 CU-h billed on Hobby stay billed; the 10 after
		// are inside Pro's allowance.
		{"upgrade keeps overage already billed", 25, 100, 40, 50, 15},
		{"within every allowance", 100, 25, 10, 20, 0},
		// At most one more allowance of the plan held now is billed.
		{"bounded by the current plan's ceiling", 100, 25, 200, 400, 25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spans := []AllowanceSpan{{End: change, Allowance: tt.before * hour}, {End: at, Allowance: tt.after * hour}}
			reader := usageBefore{change: tt.atChange * hour}
			got, err := chargeBySpan(context.Background(), reader, "acct", month, spans, UsageSnapshot{ComputeUnitSeconds: tt.total * hour}, false)
			if err != nil {
				t.Fatal(err)
			}
			if got.billedUnitSeconds != tt.wantBilled*hour || got.millicents != tt.wantBilled*api.ManagedPostgresOverageMillicentsPerComputeUnitHour {
				t.Fatalf("billed %d CU-s for %d millicents, want %d CU-h", got.billedUnitSeconds, got.millicents, tt.wantBilled)
			}
		})
	}
}

// spanUsageStore is overageUsageStore with usage before a plan change.
type spanUsageStore struct {
	*overageUsageStore
	before map[time.Time]int64
}

func (s *spanUsageStore) UsageBefore(_ context.Context, _ string, month, before time.Time) (UsageSnapshot, error) {
	return UsageSnapshot{PeriodStart: monthStart(month), ComputeUnitSeconds: s.before[before]}, nil
}

// The biller charges a downgraded subject for usage above the smaller
// allowance after the downgrade, not for the larger allowance's usage.
func TestOverageBillerBillsEachPlanSpan(t *testing.T) {
	ctx := context.Background()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, november.Add(20*24*time.Hour))
	change := november.Add(15 * 24 * time.Hour)
	f.limits.Allowance = testAllowance // Hobby now
	store := &spanUsageStore{overageUsageStore: f.store, before: map[time.Time]int64{change: 90 * 3600}}
	f.store.used["acct2026-11"] = 100 * 3600
	f.reachAllowance(t, "acct", november, true)
	options := OverageBillerOptions{Provider: "polar", EffectiveFrom: november, Now: func() time.Time { return f.now },
		Allowance: func(context.Context, string, time.Time) (CapLimits, error) { return f.limits, nil },
		PlanSpans: func(_ context.Context, _ string, _, at time.Time) ([]AllowanceSpan, error) {
			return []AllowanceSpan{{End: change, Allowance: 100 * 3600}, {End: at, Allowance: testAllowance}}, nil
		},
		Publish: func(_ context.Context, step OverageChargeStep) error {
			f.published = append(f.published, step)
			return nil
		}}
	biller, err := NewOverageBiller(store, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := biller.Deliver(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.published) != 1 || f.published[0].BilledUnitSeconds != 10*3600 || f.published[0].TargetMillicents != 10*api.ManagedPostgresOverageMillicentsPerComputeUnitHour {
		t.Fatalf("published %+v, want 10 CU-h above Hobby's allowance after the downgrade", f.published)
	}
}

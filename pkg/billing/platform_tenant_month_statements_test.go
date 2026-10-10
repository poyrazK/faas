package billing

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func tenantBucket(appID, consumer string, minute time.Time, units int64) state.APIConsumerUsageBucket {
	return state.APIConsumerUsageBucket{AppID: appID, ConsumerKey: consumer, WindowStart: minute, RequestCount: units, BillableUnits: units}
}

type lineSummary struct {
	units, amount int64
}

func linesBy(t *testing.T, in state.PlatformTenantStatementInput) map[string]lineSummary {
	t.Helper()
	out := map[string]lineSummary{}
	for _, line := range in.Lines {
		key := line.ConsumerID + "@" + time.Duration(line.PriceMillicentsPerUnit).String()
		if line.BillableUnits*line.PriceMillicentsPerUnit != line.AmountMillicents {
			t.Fatalf("line %+v is not units × price", line)
		}
		out[key] = lineSummary{line.BillableUnits, line.AmountMillicents}
	}
	return out
}

// adr: 975 — a tenant allowance counts across apps and late usage re-prices
// the month: free units move to the earlier minute and the adjustment bills
// only the difference, with a negative line but a positive total.
func TestBuildPlatformTenantMonthStatementAllowanceAdjustment(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	appA, appB := uuid.NewString(), uuid.NewString()
	card := state.PlatformTenantRateCard{ID: uuid.NewString(), Currency: "EUR", Unit: state.PlatformTenantRateCardUnitRequest,
		PriceMillicentsPerUnit: 100, IncludedUnitsPerMonth: 10, EffectiveFrom: start}
	m1, m2 := start.Add(10*time.Minute), start.Add(20*time.Minute)
	first := []state.APIConsumerUsageBucket{tenantBucket(appA, "a", m1, 6), tenantBucket(appB, "b", m2, 6)}
	rev1, err := BuildPlatformTenantMonthStatement("acct", "tenant", start, end, end, 1, "", first, first, nil,
		[]state.PlatformTenantRateCard{card}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rev1.AmountMillicents != 200 || rev1.BillableUnits != 12 || !rev1.Repriced {
		t.Fatalf("revision 1 = %+v, want 2 charged units = 200", rev1)
	}
	got := linesBy(t, rev1)
	if got["a@0s"] != (lineSummary{6, 0}) || got["b@0s"] != (lineSummary{4, 0}) || got["b@100ns"] != (lineSummary{2, 200}) {
		t.Fatalf("revision 1 lines = %+v", got)
	}
	finalized := state.PlatformTenantStatement{PeriodStart: start, PeriodEnd: end, Revision: 1,
		Status: state.APIConsumerUsageStatementFinalized, Lines: rev1.Lines}

	late := tenantBucket(appA, "a", start.Add(5*time.Minute), 3)
	month := append([]state.APIConsumerUsageBucket{late}, first...)
	rev2, err := BuildPlatformTenantMonthStatement("acct", "tenant", start, end, end, 2, state.APIConsumerUsageStatementFinalized,
		[]state.APIConsumerUsageBucket{late}, month, nil, []state.PlatformTenantRateCard{card}, []state.PlatformTenantStatement{finalized})
	if err != nil {
		t.Fatal(err)
	}
	got = linesBy(t, rev2)
	if rev2.BillableUnits != 3 || rev2.AmountMillicents != 300 || got["a@0s"] != (lineSummary{3, 0}) ||
		got["b@0s"] != (lineSummary{-3, 0}) || got["b@100ns"] != (lineSummary{3, 300}) {
		t.Fatalf("adjustment = %+v lines %+v, want +3 units billing 300", rev2, got)
	}
	if len(rev2.Coverage) != 1 || rev2.Coverage[0].BillableUnits != 3 {
		t.Fatalf("adjustment coverage = %+v, want only the late minute", rev2.Coverage)
	}
}

// adr: 975
func TestBuildPlatformTenantMonthStatementTiersAndRules(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	five := int64(5)
	card := state.PlatformTenantRateCard{ID: uuid.NewString(), Currency: "EUR", Unit: state.PlatformTenantRateCardUnitRequest,
		PriceMillicentsPerUnit: 50, Tiers: []state.APIConsumerRateCardTier{{UpTo: &five, PriceMillicentsPerUnit: 100}, {PriceMillicentsPerUnit: 50}},
		EffectiveFrom: start}
	usage := []state.APIConsumerUsageBucket{tenantBucket("app-1", "a", start.Add(time.Minute), 4), tenantBucket("app-2", "b", start.Add(2*time.Minute), 4)}
	in, err := BuildPlatformTenantMonthStatement("acct", "tenant", start, end, end, 1, "", usage, usage, nil, []state.PlatformTenantRateCard{card}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := linesBy(t, in)
	if in.AmountMillicents != 650 || got["a@100ns"] != (lineSummary{4, 400}) || got["b@100ns"] != (lineSummary{1, 100}) || got["b@50ns"] != (lineSummary{3, 150}) {
		t.Fatalf("tiered month = %+v lines %+v, want 650 across the shared ladder", in, got)
	}
	if _, err := BuildPlatformTenantMonthStatement("acct", "tenant", start, start.AddDate(0, 0, 7), end, 1, "", usage, usage, nil,
		[]state.PlatformTenantRateCard{card}, nil); !errors.Is(err, ErrTenantMonthPeriodRequired) {
		t.Fatalf("weekly period err = %v, want ErrTenantMonthPeriodRequired", err)
	}
	if _, err := BuildPlatformTenantMonthStatement("acct", "tenant", start, end, end, 1, "", nil, usage, nil,
		[]state.PlatformTenantRateCard{card}, nil); !errors.Is(err, ErrNoNewTenantUsage) {
		t.Fatalf("no delta err = %v, want ErrNoNewTenantUsage", err)
	}
	flat := state.PlatformTenantRateCard{ID: uuid.NewString(), Currency: "EUR", Unit: state.PlatformTenantRateCardUnitRequest,
		PriceMillicentsPerUnit: 10, EffectiveFrom: start.AddDate(0, 1, 0)}
	cases := []struct {
		name       string
		cards      []state.PlatformTenantRateCard
		start, end time.Time
		want       bool
	}{
		{"monthly card in force", []state.PlatformTenantRateCard{card}, start, end, true},
		{"superseded before the period", []state.PlatformTenantRateCard{card, flat}, end, end.AddDate(0, 1, 0), false},
		{"flat only", []state.PlatformTenantRateCard{flat}, start, end.AddDate(0, 1, 0), false},
		{"not yet effective", []state.PlatformTenantRateCard{card}, start.AddDate(0, -1, 0), start, false},
	}
	for _, tc := range cases {
		if got := TenantMonthlyPricing(tc.cards, tc.start, tc.end); got != tc.want {
			t.Errorf("%s: TenantMonthlyPricing = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// adr: 975 — an app card's allowance blocks a cross-app statement only for
// minutes no tenant card prices.
func TestQuotePlatformTenantUsageAppAllowanceOnlyOnFallback(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	appCards := []state.APIConsumerRateCard{{ID: uuid.NewString(), Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest,
		PriceMillicentsPerUnit: 5, IncludedUnitsPerMonth: 100, EffectiveFrom: start}}
	tenantCards := []state.PlatformTenantRateCard{{ID: uuid.NewString(), Currency: "EUR", Unit: state.PlatformTenantRateCardUnitRequest,
		PriceMillicentsPerUnit: 7, EffectiveFrom: start.Add(time.Hour)}}
	covered := []state.APIConsumerUsageBucket{tenantBucket("app", "a", start.Add(2*time.Hour), 3)}
	if quote, err := QuotePlatformTenantUsage(appCards, tenantCards, covered); err != nil || quote.AmountMillicents != 21 {
		t.Fatalf("tenant-priced quote = %+v err=%v, want 21", quote, err)
	}
	early := []state.APIConsumerUsageBucket{tenantBucket("app", "a", start.Add(time.Minute), 3)}
	if _, err := QuotePlatformTenantUsage(appCards, tenantCards, early); !errors.Is(err, ErrAPIConsumerAllowanceInTenantStatement) {
		t.Fatalf("fallback to an allowance card err = %v, want ErrAPIConsumerAllowanceInTenantStatement", err)
	}
}

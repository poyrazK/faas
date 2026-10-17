package billing

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func volumeCard(from time.Time) state.APIConsumerRateCard {
	card := volumeLadder(from)
	card.TierMode = state.RateCardTierModeVolume
	return card
}

// adr: 1309 — a volume ladder prices every unit of the month at the step
// the month's total reaches; a month that stays in the free step is free.
func TestQuotePricesWholeMonthAtVolumeStep(t *testing.T) {
	cards := []state.APIConsumerRateCard{volumeCard(allowanceStart)}
	nextMonth := allowanceStart.AddDate(0, 1, 0)
	quote, err := QuoteAPIConsumerUsage(cards, []state.APIConsumerUsageBucket{
		usageAt(allowanceStart, 3),
		usageAt(allowanceStart.Add(time.Minute), 9), // month total 12: every unit at 10
		usageAt(nextMonth, 4),                       // month total 4: free
		usageAt(nextMonth.AddDate(0, 1, 0), 10),     // exactly 10: the 100 step
	})
	if err != nil {
		t.Fatal(err)
	}
	wantTiers := [][]int64{{0, 0, 3}, {0, 0, 9}, {4, 0, 0}, {0, 10, 0}}
	wantAmounts := []int64{30, 90, 0, 1000}
	for i, b := range quote.Buckets {
		if !slices.Equal(b.TierUnits, wantTiers[i]) || b.AmountMillicents != wantAmounts[i] {
			t.Fatalf("bucket %d = %v / %d, want %v / %d", i, b.TierUnits, b.AmountMillicents, wantTiers[i], wantAmounts[i])
		}
	}
	if !slices.Equal(chargedUnits(quote), []int64{3, 9, 0, 10}) || quote.AmountMillicents != 1120 {
		t.Fatalf("quote = %+v", quote)
	}
}

// adr: 1309 — late usage within the billed step adds only its own charge;
// late usage that moves the month to a cheaper step would lower the total,
// which Gregale refuses rather than crediting.
func TestVolumeStatementDelta(t *testing.T) {
	cards := []state.APIConsumerRateCard{volumeCard(allowanceStart)}
	m0, m1 := allowanceStart, allowanceStart.Add(time.Minute)
	billed, err := QuoteAPIConsumerUsage(cards, []state.APIConsumerUsageBucket{usageAt(m0, 3), usageAt(m1, 4)})
	if err != nil || billed.AmountMillicents != 700 {
		t.Fatalf("billed = %+v, %v; want 7 units at 100", billed, err)
	}
	prior := []state.APIConsumerUsageStatement{finalizedFrom(billed)}
	current, err := QuoteAPIConsumerUsage(cards, []state.APIConsumerUsageBucket{usageAt(m0, 4), usageAt(m1, 4)})
	if err != nil {
		t.Fatal(err)
	}
	delta, err := APIConsumerStatementDelta(current, prior)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Buckets) != 1 || delta.BillableUnits != 1 || delta.AmountMillicents != 100 {
		t.Fatalf("delta = %+v, want the late unit at 100", delta)
	}
	cheaper, err := QuoteAPIConsumerUsage(cards, []state.APIConsumerUsageBucket{usageAt(m0, 7), usageAt(m1, 4)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := APIConsumerStatementDelta(cheaper, prior); !errors.Is(err, ErrAPIConsumerChargeDecreased) {
		t.Fatalf("delta into a cheaper step: %v, want ErrAPIConsumerChargeDecreased", err)
	}
	// adr: 1312 — keeping the billed step bills the late units at its price.
	locked := LockedVolumeSteps(cards, prior)
	if locked[cards[0].ID] != 1 {
		t.Fatalf("locked steps = %v, want step 1", locked)
	}
	kept, err := QuoteAPIConsumerUsageWith(cards, []state.APIConsumerUsageBucket{usageAt(m0, 7), usageAt(m1, 4)}, time.Time{},
		QuoteOptions{VolumeSteps: locked})
	if err != nil {
		t.Fatal(err)
	}
	delta, err = APIConsumerStatementDelta(kept, prior)
	if err != nil || delta.BillableUnits != 4 || delta.AmountMillicents != 400 {
		t.Fatalf("locked delta = %+v, %v; want 4 late units at 100", delta, err)
	}
}

// adr: 1312 — a tenant volume month keeps its billed step when late usage
// would move it to a cheaper one.
func TestLockedTenantVolumeStep(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	app := uuid.NewString()
	ladder := volumeCard(start).Tiers
	card := state.PlatformTenantRateCard{ID: uuid.NewString(), Currency: "EUR", Unit: state.PlatformTenantRateCardUnitRequest,
		Tiers: ladder, TierMode: state.RateCardTierModeVolume, PriceMillicentsPerUnit: 10, EffectiveFrom: start}
	minute := start.Add(time.Hour)
	first := []state.APIConsumerUsageBucket{tenantBucket(app, "c1", minute, 7)}
	rev1, err := BuildPlatformTenantMonthStatement("acct", "tenant", start, end, end, 1, "", first, first, nil,
		[]state.PlatformTenantRateCard{card}, nil, nil)
	if err != nil || rev1.AmountMillicents != 700 {
		t.Fatalf("revision 1 = %+v, %v", rev1, err)
	}
	finalized := []state.PlatformTenantStatement{{Status: state.APIConsumerUsageStatementFinalized, PeriodStart: start, PeriodEnd: end,
		BillableUnits: rev1.BillableUnits, AmountMillicents: rev1.AmountMillicents, Lines: rev1.Lines, Coverage: rev1.Coverage}}
	late := []state.APIConsumerUsageBucket{tenantBucket(app, "c1", minute.Add(time.Minute), 5)}
	month := append(first, late...)
	if _, err := BuildPlatformTenantMonthStatement("acct", "tenant", start, end, end, 2, state.APIConsumerUsageStatementFinalized,
		late, month, nil, []state.PlatformTenantRateCard{card}, finalized, nil); !errors.Is(err, ErrTenantChargeDecreased) {
		t.Fatalf("exact re-pricing: %v, want ErrTenantChargeDecreased", err)
	}
	locked := LockedTenantVolumeSteps([]state.PlatformTenantRateCard{card}, finalized, start, end)
	rev2, err := BuildPlatformTenantMonthStatement("acct", "tenant", start, end, end, 2, state.APIConsumerUsageStatementFinalized,
		late, month, nil, []state.PlatformTenantRateCard{card}, finalized, nil, QuoteOptions{VolumeSteps: locked})
	if err != nil || rev2.BillableUnits != 5 || rev2.AmountMillicents != 500 {
		t.Fatalf("locked revision = %+v, %v; want 5 late units at 100", rev2, err)
	}
}

// adr: 1310 — a tenant compute card prices each subject minute's compute
// time in whole seconds, surfaces included, and late compute bills the
// rounded difference.
func TestBuildPlatformTenantMonthStatementComputeSeconds(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	app := uuid.NewString()
	card := state.PlatformTenantRateCard{ID: uuid.NewString(), Currency: "EUR", Unit: state.APIConsumerRateCardUnitComputeSecond,
		PriceMillicentsPerUnit: 100, EffectiveFrom: start}
	if !card.MonthlyPricing() {
		t.Fatal("a compute tenant card must re-price whole months")
	}
	minute := start.Add(10 * time.Minute)
	consumer := tenantBucket(app, "c1", minute, 2)
	consumer.ComputeMS = 1500
	surface := state.APIConsumerUsageBucket{AppID: app, SurfaceID: "surface-1", WindowStart: minute, RequestCount: 1, BillableUnits: 1, ComputeMS: 2400}
	first := []state.APIConsumerUsageBucket{consumer, surface}
	rev1, err := BuildPlatformTenantMonthStatement("acct", "tenant", start, end, end, 1, "", first, first, nil,
		[]state.PlatformTenantRateCard{card}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// c1: 1.5 s rounds to 2; the surface's 2.4 s rounds to 2.
	if rev1.BillableUnits != 4 || rev1.AmountMillicents != 400 || !rev1.Weighted {
		t.Fatalf("revision 1 = %+v", rev1)
	}
	finalized := state.PlatformTenantStatement{Status: state.APIConsumerUsageStatementFinalized, PeriodStart: start, PeriodEnd: end,
		BillableUnits: rev1.BillableUnits, AmountMillicents: rev1.AmountMillicents, Lines: rev1.Lines, Coverage: rev1.Coverage}
	late := tenantBucket(app, "c1", minute, 1)
	grown := consumer
	grown.RequestCount, grown.BillableUnits, grown.ComputeMS = 3, 3, 2500
	rev2, err := BuildPlatformTenantMonthStatement("acct", "tenant", start, end, end, 2, state.APIConsumerUsageStatementFinalized,
		[]state.APIConsumerUsageBucket{late}, []state.APIConsumerUsageBucket{grown, surface}, nil,
		[]state.PlatformTenantRateCard{card}, []state.PlatformTenantStatement{finalized}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rev2.BillableUnits != 1 || rev2.AmountMillicents != 100 {
		t.Fatalf("revision 2 = %+v, want c1's minute to grow from 2 to 3 seconds", rev2)
	}
	if _, err := QuotePlatformTenantUsage(nil, []state.PlatformTenantRateCard{card}, first); !errors.Is(err, ErrTenantMonthPeriodRequired) {
		t.Fatalf("delta quote of a compute card: %v, want ErrTenantMonthPeriodRequired", err)
	}
}

// adr: 1310 — tenant route weights apply to surface and JWT-rule requests
// through their own route counts.
func TestBuildPlatformTenantMonthStatementWeightsSourceRoutes(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	app := uuid.NewString()
	card := state.PlatformTenantRateCard{ID: uuid.NewString(), Currency: "EUR", Unit: state.PlatformTenantRateCardUnitRequest,
		PriceMillicentsPerUnit: 100, RouteWeights: map[string]int64{"POST /generate": 5}, EffectiveFrom: start}
	minute := start.Add(10 * time.Minute)
	surface := state.APIConsumerUsageBucket{AppID: app, SurfaceID: "surface-1", WindowStart: minute, RequestCount: 3, BillableUnits: 3}
	jwt := state.APIConsumerUsageBucket{AppID: app, JWTAuthorizationRuleID: "rule-1", WindowStart: minute, RequestCount: 2, BillableUnits: 2}
	usage := []state.APIConsumerUsageBucket{surface, jwt}
	routes := TenantRouteUsage{
		TenantSourceRouteKey(app, "surface", "surface-1"): {{WindowStart: minute, Route: "POST /generate", BillableUnits: 2}},
		TenantSourceRouteKey(app, "jwt", "rule-1"):        {{WindowStart: minute, Route: "GET /status", BillableUnits: 2}},
	}
	rev, err := BuildPlatformTenantMonthStatement("acct", "tenant", start, end, end, 1, "", usage, usage, nil,
		[]state.PlatformTenantRateCard{card}, nil, routes)
	if err != nil {
		t.Fatal(err)
	}
	// Surface: 1 + 2 × 5 = 11; the JWT rule's unweighted route counts 2.
	if rev.BillableUnits != 13 || rev.AmountMillicents != 1300 {
		t.Fatalf("revision = %+v", rev)
	}
}

// adr: 1314 — trial minutes are priced at zero and consume no included
// units, so the allowance starts counting after the trial.
func TestQuotePricesTrialMinutesFree(t *testing.T) {
	cards := []state.APIConsumerRateCard{allowanceCard(allowanceStart, 100, 5)}
	trialEnd := allowanceStart.Add(2 * time.Minute)
	quote, err := QuoteAPIConsumerUsageWith(cards, []state.APIConsumerUsageBucket{
		usageAt(allowanceStart, 7),
		usageAt(allowanceStart.Add(time.Minute), 3),
		usageAt(trialEnd, 8), // 5 free, 3 at 100
	}, time.Time{}, QuoteOptions{TrialStart: allowanceStart, TrialEnd: trialEnd})
	if err != nil {
		t.Fatal(err)
	}
	if !quote.Buckets[0].Trial || !quote.Buckets[1].Trial || quote.Buckets[2].Trial || quote.Buckets[0].RateCardID == "" {
		t.Fatalf("buckets = %+v", quote.Buckets)
	}
	if !slices.Equal(chargedUnits(quote), []int64{0, 0, 3}) || quote.AmountMillicents != 300 || !quote.Priced {
		t.Fatalf("quote = %+v", quote)
	}
}

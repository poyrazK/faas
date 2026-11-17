package billing

// Helpers of platform tenant month statements (ADR-975, ADR-1322) whose
// branches the weekly mutation run (make test-mutation-billing) showed no test
// noticed changing.

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestAddsWithinInt64Edges(t *testing.T) {
	cases := []struct {
		total, delta int64
		want         bool
	}{
		{math.MinInt64, 0, true},
		{maxInt64 - 5, 5, true},
		{maxInt64 - 4, 5, false},
		{-maxInt64 + 5, -5, true},
		{-maxInt64 + 4, -5, false},
	}
	for _, tc := range cases {
		if got := addsWithinInt64(tc.total, tc.delta); got != tc.want {
			t.Fatalf("addsWithinInt64(%d, %d) = %v, want %v", tc.total, tc.delta, got, tc.want)
		}
	}
}

// A line's window spans its earliest minute to the end of its latest, and an
// overflowing unit count is refused even when the amount fits.
func TestTenantLineAddWindowAndOverflow(t *testing.T) {
	minute := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	var line tenantLine
	for _, at := range []time.Time{minute.Add(time.Minute), minute, minute.Add(5 * time.Minute)} {
		if err := line.add(1, 10, at); err != nil {
			t.Fatal(err)
		}
	}
	if !line.line.WindowStart.Equal(minute) || !line.line.WindowEnd.Equal(minute.Add(6*time.Minute)) {
		t.Fatalf("window = %v..%v, want %v..%v", line.line.WindowStart, line.line.WindowEnd, minute, minute.Add(6*time.Minute))
	}
	full := tenantLine{line: state.PlatformTenantStatementLine{BillableUnits: maxInt64}}
	if err := full.add(1, 0, minute); err == nil {
		t.Fatal("unit overflow accepted")
	}
}

func TestTenantSubjectRouteKeyKinds(t *testing.T) {
	for name, bucket := range map[string]state.APIConsumerUsageBucket{
		"consumer": {AppID: "app", ConsumerKey: "c"},
		"surface":  {AppID: "app", SurfaceID: "s"},
		"jwt":      {AppID: "app", JWTAuthorizationRuleID: "r"},
	} {
		if tenantSubjectRouteKey(bucket) == "" {
			t.Fatalf("%s subject has no route key", name)
		}
	}
	if key := tenantSubjectRouteKey(state.APIConsumerUsageBucket{AppID: "app"}); key != "" {
		t.Fatalf("anonymous bucket key = %q, want none", key)
	}
}

// A request card without route weights does not weight a month.
func TestTenantWeightsInForceOnlyForWeightedOrOtherUnits(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	card := state.PlatformTenantRateCard{ID: "req", Currency: "EUR", Unit: state.PlatformTenantRateCardUnitRequest, PriceMillicentsPerUnit: 1, EffectiveFrom: start}
	if tenantWeightsInForce([]state.PlatformTenantRateCard{card}, start, start.AddDate(0, 1, 0)) {
		t.Fatal("an unweighted request card weights the month")
	}
	card.RouteWeights = map[string]int64{"/heavy": 3}
	if !tenantWeightsInForce([]state.PlatformTenantRateCard{card}, start, start.AddDate(0, 1, 0)) {
		t.Fatal("a weighted request card does not weight the month")
	}
}

// A fee-only revision keeps the currency it was given, falls back to the
// tenant cards', and may total zero.
func TestTenantFeeOnlyStatementCurrencyAndZero(t *testing.T) {
	cards := []state.PlatformTenantRateCard{{ID: "t", Currency: "EUR"}}
	in := state.PlatformTenantStatementInput{Fees: []state.APIConsumerStatementFee{{Kind: state.StatementFeeBase, AmountMillicents: 100}}}
	if got, err := tenantFeeOnlyStatement(in, "USD", cards); err != nil || got.Currency != "USD" || got.AmountMillicents != 100 {
		t.Fatalf("given currency = %+v, %v", got, err)
	}
	if got, err := tenantFeeOnlyStatement(in, "", cards); err != nil || got.Currency != "EUR" {
		t.Fatalf("card currency = %+v, %v", got, err)
	}
	if got, err := tenantFeeOnlyStatement(in, "", nil); err != nil || got.Currency != "" {
		t.Fatalf("no cards = %+v, %v", got, err)
	}
	zero := state.PlatformTenantStatementInput{Fees: []state.APIConsumerStatementFee{{Kind: state.StatementFeeBase}}}
	if _, err := tenantFeeOnlyStatement(zero, "EUR", cards); err != nil {
		t.Fatalf("zero fee-only revision refused: %v", err)
	}
	negative := state.PlatformTenantStatementInput{Fees: []state.APIConsumerStatementFee{{Kind: state.StatementFeeMinimum, AmountMillicents: -1}}}
	if _, err := tenantFeeOnlyStatement(negative, "EUR", cards); !errors.Is(err, ErrTenantChargeDecreased) {
		t.Fatalf("negative fee-only err = %v", err)
	}
}

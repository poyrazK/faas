package billing

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

func forecastMeter(available bool, net, gross *int64) api.FinancialMeterCosts {
	return api.FinancialMeterCosts{Forecast: financial.Forecast{Available: available, ProjectedNetMillicents: net, ProjectedGrossMillicents: gross}}
}

func ptr(v int64) *int64 { return &v }

// TestProjectedSubtotal is issue #264: the plan fee plus every meter's
// projection, and nothing when any meter cannot be projected.
func TestProjectedSubtotal(t *testing.T) {
	fee := api.MustLimitsFor(api.PlanHobby).PriceMillicents
	for name, tc := range map[string]struct {
		meters []api.FinancialMeterCosts
		want   *int64
	}{
		"fee plus net and gross meters": {
			meters: []api.FinancialMeterCosts{forecastMeter(true, ptr(250_000), nil), forecastMeter(true, nil, ptr(40_000))},
			want:   ptr(fee + 290_000),
		},
		"zero usage is just the fee":  {meters: []api.FinancialMeterCosts{forecastMeter(true, ptr(0), nil)}, want: ptr(fee)},
		"one meter unavailable":       {meters: []api.FinancialMeterCosts{forecastMeter(true, ptr(1), nil), forecastMeter(false, nil, nil)}},
		"available without an amount": {meters: []api.FinancialMeterCosts{forecastMeter(true, nil, nil)}},
		"no meters":                   {},
	} {
		t.Run(name, func(t *testing.T) {
			got := ProjectedSubtotal(api.PlanHobby, tc.meters)
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("ProjectedSubtotal = %v, want %v", got, tc.want)
			}
		})
	}
	if got := ProjectedSubtotal(api.Plan("nope"), []api.FinancialMeterCosts{forecastMeter(true, ptr(1), nil)}); got != nil {
		t.Fatalf("unknown plan subtotal = %d, want nil", *got)
	}
}

func TestForecastMissingComponents(t *testing.T) {
	missing := []string{"subscription_and_addons", "credits_and_adjustments", "tax"}
	if got := ForecastMissingComponents(missing, ptr(1)); !slices.Equal(got, []string{"addons", "credits_and_adjustments", "tax"}) {
		t.Fatalf("with subtotal = %v", got)
	}
	if got := ForecastMissingComponents(missing, nil); !slices.Equal(got, missing) {
		t.Fatalf("without subtotal = %v", got)
	}
}

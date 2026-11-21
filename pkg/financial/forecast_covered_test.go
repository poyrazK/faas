package financial

import (
	"testing"
	"time"
)

// Cost-control review 2026-11-19: a forecast over a partly sampled period
// extrapolates over the covered time, so a lost minute neither hides the
// forecast nor understates it.
func TestForecastMeterOverCovered(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	through := start.Add(2 * 24 * time.Hour)
	p := Price{Version: "v1", Meter: "compute", Currency: BillingCurrency, Unit: "unit", UnitQuantity: 1, MillicentsPerUnit: 1}

	whole, err := ForecastMeterOverCovered("account", start, end, through, through.Sub(start), 2880, p, true)
	if err != nil || !whole.Available || whole.Method != "elapsed_time_run_rate_v1" || *whole.ProjectedQuantity != 44640 {
		t.Fatalf("whole coverage = %+v %v", whole, err)
	}
	// One minute lost: 2,879 units in 2,879 covered minutes still projects
	// one unit a minute.
	gap, err := ForecastMeterOverCovered("account", start, end, through, through.Sub(start)-time.Minute, 2879, p, true)
	if err != nil || !gap.Available || gap.Method != "covered_time_run_rate_v1" || *gap.ProjectedQuantity != 44640 {
		t.Fatalf("one lost minute = %+v %v", gap, err)
	}
	for _, covered := range []time.Duration{0, through.Sub(start) + time.Minute} {
		if got, err := ForecastMeterOverCovered("account", start, end, through, covered, 10, p, true); err != nil || got.Available || got.Reason != "incomplete_coverage" {
			t.Fatalf("covered %v = %+v %v; want incomplete_coverage", covered, got, err)
		}
	}
}

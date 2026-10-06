package financial

import (
	"errors"
	"math"
	"testing"
	"time"
)

// adr: 566 — forecast quantities before applying the shared allowance.
func TestForecastMeterPricesProjectedQuantity(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	p := Price{Version: "v1", Meter: "compute", Currency: "EUR", Unit: "unit", UnitQuantity: 1, MillicentsPerUnit: 2, IncludedQuantity: 100}
	got, err := ForecastMeter("account", start, end, start.Add(2*24*time.Hour), 10, p, true, true)
	if err != nil || !got.Available || *got.ProjectedQuantity != 155 || *got.ProjectedNetMillicents != 110 {
		t.Fatalf("forecast=%+v %v", got, err)
	}
	got, err = ForecastMeter("account", start, end, end, 10, p, true, true)
	if err != nil || *got.ProjectedQuantity != 10 || *got.ProjectedNetMillicents != 0 {
		t.Fatalf("closed period=%+v %v", got, err)
	}
}

// adr: 566 — missing, stale and insufficient evidence is not zero spend.
func TestForecastMeterUnavailable(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	p := Price{Version: "v1", Meter: "compute", Currency: "EUR", Unit: "unit", UnitQuantity: 1, MillicentsPerUnit: 1}
	for _, tc := range []struct {
		name            string
		through         time.Time
		complete, fresh bool
	}{
		{"incomplete_coverage", start.Add(24 * time.Hour), false, true},
		{"stale_evidence", start.Add(24 * time.Hour), true, false},
		{"insufficient_history", start.Add(time.Hour), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ForecastMeter("account", start, end, tc.through, 10, p, tc.complete, tc.fresh)
			if err != nil || got.Available || got.Reason != tc.name || got.ProjectedNetMillicents != nil {
				t.Fatalf("forecast=%+v %v", got, err)
			}
		})
	}
	if _, err := ForecastMeter("account", start, end, start.Add(24*time.Hour), math.MaxInt64, p, true, true); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflow=%v", err)
	}
}

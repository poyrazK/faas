package meter

// spec: §4.7
// The savings estimate prices the same billable unit as metering: plan
// RAM + 8 MB per running second.

import (
	"testing"
	"time"
)

var savingsDay = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

// TestScaleToZeroSavings_ParkedMostOfTheDay pins the headline case: a
// 512 MB app (520 MB billable) that ran one hour out of 24 saves the
// other 23 hours of always-on RAM.
func TestScaleToZeroSavings_ParkedMostOfTheDay(t *testing.T) {
	got := ScaleToZeroSavings(SavingsInput{
		Since:                    savingsDay,
		Until:                    savingsDay.Add(24 * time.Hour),
		BillableRAMMB:            520,
		ActualMBSeconds:          520 * 3600,
		PriceMillicentsPerGBHour: 1_000,
	})
	if got.WindowSeconds != 86_400 {
		t.Fatalf("window = %d s, want 86400", got.WindowSeconds)
	}
	if got.AlwaysOnMBSeconds != 520*86_400 {
		t.Fatalf("always-on = %d MB-s, want %d", got.AlwaysOnMBSeconds, 520*86_400)
	}
	if got.SavedMBSeconds != 520*23*3600 {
		t.Fatalf("saved = %d MB-s, want %d", got.SavedMBSeconds, 520*23*3600)
	}
	// 520 MB × 23 h = 11.679688 GB-h → 11,680 millicents at €0.01/GB-h.
	if got.SavedGBHours != 11.679688 {
		t.Errorf("saved GB-h = %v, want 11.679688", got.SavedGBHours)
	}
	if got.SavedMillicents != 11_680 {
		t.Errorf("saved millicents = %d, want 11680", got.SavedMillicents)
	}
	if got.AlwaysOnMillicents != got.ActualMillicents+got.SavedMillicents {
		t.Errorf("always-on %d != actual %d + saved %d", got.AlwaysOnMillicents, got.ActualMillicents, got.SavedMillicents)
	}
	if got.ParkedRatio != 0.958333 {
		t.Errorf("parked ratio = %v, want 0.958333", got.ParkedRatio)
	}
}

// TestScaleToZeroSavings_FloorInstancesScaleTheCounterfactual pins that a
// min_instances floor multiplies the always-on baseline, and that a
// floor of zero still counts one resident instance.
func TestScaleToZeroSavings_FloorInstancesScaleTheCounterfactual(t *testing.T) {
	base := SavingsInput{Since: savingsDay, Until: savingsDay.Add(time.Hour), BillableRAMMB: 1032}
	for _, tc := range []struct {
		floor, wantInstances int
	}{{0, 1}, {1, 1}, {3, 3}} {
		in := base
		in.FloorInstances = tc.floor
		got := ScaleToZeroSavings(in)
		if want := int64(1032 * 3600 * tc.wantInstances); got.AlwaysOnMBSeconds != want {
			t.Errorf("floor %d: always-on = %d, want %d", tc.floor, got.AlwaysOnMBSeconds, want)
		}
	}
}

// TestScaleToZeroSavings_BurstAboveBaselineClampsToZero pins the
// conservative clamp: an app whose burst ran more RAM-time than one
// always-on instance reports zero savings, never a negative number.
func TestScaleToZeroSavings_BurstAboveBaselineClampsToZero(t *testing.T) {
	got := ScaleToZeroSavings(SavingsInput{
		Since:                    savingsDay,
		Until:                    savingsDay.Add(time.Hour),
		BillableRAMMB:            520,
		ActualMBSeconds:          520 * 3600 * 2,
		PriceMillicentsPerGBHour: 1_000,
	})
	if got.SavedMBSeconds != 0 || got.SavedMillicents != 0 || got.ParkedRatio != 0 {
		t.Fatalf("burst saving = %+v, want zero saving", got)
	}
	if got.ActualMBSeconds != 520*3600*2 {
		t.Errorf("actual not echoed: %d", got.ActualMBSeconds)
	}
}

// TestScaleToZeroSavings_DegenerateInputs pins the fail-safe branches:
// an empty or inverted window, or a missing RAM size, produce no
// counterfactual instead of a panic or a negative figure.
func TestScaleToZeroSavings_DegenerateInputs(t *testing.T) {
	for name, in := range map[string]SavingsInput{
		"empty window":    {Since: savingsDay, Until: savingsDay, BillableRAMMB: 520},
		"inverted window": {Since: savingsDay.Add(time.Hour), Until: savingsDay, BillableRAMMB: 520},
		"zero ram":        {Since: savingsDay, Until: savingsDay.Add(time.Hour)},
		"negative actual": {Since: savingsDay, Until: savingsDay, BillableRAMMB: 520, ActualMBSeconds: -5},
	} {
		got := ScaleToZeroSavings(in)
		if got.AlwaysOnMBSeconds != 0 || got.SavedMBSeconds != 0 || got.ActualMBSeconds < 0 {
			t.Errorf("%s: got %+v, want zero counterfactual", name, got)
		}
	}
}

// TestValueMillicents_RoundsHalfUpInIntegers pins the money rounding:
// half a millicent rounds up, anything below rounds down, and a zero
// price values nothing.
func TestValueMillicents_RoundsHalfUpInIntegers(t *testing.T) {
	if got := valueMillicents(mbSecondsPerGBHour, 1_000); got != 1_000 {
		t.Errorf("1 GB-h = %d, want 1000", got)
	}
	if got := valueMillicents(1, mbSecondsPerGBHour/2); got != 1 {
		t.Errorf("0.5 millicent = %d, want 1 (half up)", got)
	}
	if got := valueMillicents(1, mbSecondsPerGBHour/2-1); got != 0 {
		t.Errorf("just under 0.5 millicent = %d, want 0", got)
	}
	if got := valueMillicents(mbSecondsPerGBHour, 0); got != 0 {
		t.Errorf("zero price = %d, want 0", got)
	}
}

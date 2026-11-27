package managedpostgres

import "testing"

func TestAutoGrowTargetBounds(t *testing.T) {
	const gib = 1 << 30
	for _, tc := range []struct{ size, ceiling, want int64 }{
		{size: gib, ceiling: 3 * gib, want: 2 * gib},
		{size: 8 * gib, ceiling: 20 * gib, want: 10 * gib},
		{size: 8 * gib, ceiling: 9 * gib, want: 9 * gib},
		{size: 8 * gib, ceiling: 8 * gib, want: 0},
		{size: 8 * gib, ceiling: 0, want: 0},
	} {
		if got := autoGrowTarget(tc.size, tc.ceiling); got != tc.want {
			t.Errorf("size %d, ceiling %d: got %d, want %d", tc.size, tc.ceiling, got, tc.want)
		}
	}
}

func TestAutoGrowEligibility(t *testing.T) {
	d := Database{Spec: Spec{Availability: AvailabilitySingleZone, ScaleToZero: true}}
	if !autoGrowEligible(d) {
		t.Fatal("single-node scale-to-zero database was ineligible")
	}
	d.Spec.Availability = AvailabilityHighlyAvailable
	if autoGrowEligible(d) {
		t.Fatal("HA database was eligible")
	}
	d.Spec.Availability, d.Spec.ScaleToZero = AvailabilitySingleZone, false
	if !autoGrowEligible(d) {
		t.Fatal("single-node always-on database could not set a storage ceiling")
	}
}

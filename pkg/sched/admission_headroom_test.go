// adr: 909

package sched_test

import (
	"testing"
)

// The floor trigger pre-checked against the fleet-sum HeadroomMB, which goes
// to zero once measured admission lets a node's reservations pass its bare
// ceiling. Every floor then reported ram_ceiling while Admit would have
// placed it, and meterd still billed the gap (bug hunt 2026-10-27 B2).
func TestAdmissionHeadroomFollowsMeasuredAdmission(t *testing.T) {
	m := &fakeMeasurement{mb: map[string]int{}, ok: true}
	l, clock := newMeasuredLedger(m)
	// Reservations to 150% of the ceiling (12 × 1032 MB on 8,593 MB),
	// measured at about 40%.
	for i := range 12 {
		clock.tick()
		m.mb["n1"] = measuredCeilingMB * 40 / 100
		m.asOf = clock.t
		clock.tick()
		if err := l.Admit(measuredScaleReq(i)); err != nil {
			t.Fatalf("admit %d: %v", i, err)
		}
	}
	clock.tick()
	m.asOf = clock.t
	if got := l.HeadroomMB(); got != 0 {
		t.Fatalf("fleet-sum headroom = %d, want 0 (reservations past the bare ceiling)", got)
	}
	head := l.AdmissionHeadroomMB()
	if head < 1032 {
		t.Fatalf("admission headroom = %d MB; a 1 GiB floor fits under the measured figure", head)
	}
	if err := l.Admit(measuredScaleReq(12)); err != nil {
		t.Fatalf("Admit refused what the headroom allowed: %v", err)
	}
}

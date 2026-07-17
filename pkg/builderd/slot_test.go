package builderd

import "testing"

type fakeResid struct{ mb int }

func (f fakeResid) ResidentMB() int { return f.mb }

func TestSlot_NilProbeGuaranteedOnly(t *testing.T) {
	got := DecideSlot(nil, 47_600)
	if !got.Allowed || got.Label != "guaranteed" {
		t.Errorf("nil probe: got %+v, want guaranteed allowed", got)
	}
}

func TestSlot_BelowThresholdGetsOpportunistic(t *testing.T) {
	// 50% residency → opportunistic slot granted.
	got := DecideSlot(fakeResid{mb: 23_000}, 47_600)
	if !got.Allowed || got.Label != "opportunistic" {
		t.Errorf("50%% residency: got %+v, want opportunistic", got)
	}
}

func TestSlot_AtThresholdGuaranteedOnly(t *testing.T) {
	// 60% residency → threshold (>=); opportunistic denied, guaranteed allowed.
	got := DecideSlot(fakeResid{mb: 28_560}, 47_600)
	if !got.Allowed || got.Label != "guaranteed" {
		t.Errorf("60%% residency: got %+v, want guaranteed", got)
	}
}

func TestSlot_AboveThresholdGuaranteedOnly(t *testing.T) {
	// 80% residency → guaranteed only.
	got := DecideSlot(fakeResid{mb: 38_000}, 47_600)
	if !got.Allowed || got.Label != "guaranteed" {
		t.Errorf("80%% residency: got %+v, want guaranteed", got)
	}
}

func TestSlot_ZeroCeilingFallsBack(t *testing.T) {
	got := DecideSlot(fakeResid{mb: 1000}, 0)
	if !got.Allowed || got.Label != "guaranteed" {
		t.Errorf("zero ceiling: got %+v, want guaranteed (fallback)", got)
	}
}

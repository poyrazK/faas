package wire_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

// ForgetApp deletes an app's series under both label spellings and leaves
// other apps' series (ADR 20261103131546).
func TestOpsMetricsForgetAppDeletesOnlyThatApp(t *testing.T) {
	m := wire.NewOpsMetrics("forget_app_test")
	m.ObserveScaleUp("gone", "admit")
	m.ObserveScaleUp("gone", "no_signal")
	m.ObserveFloor("gone", "floor_met")
	m.SetEgressCircuitState("gone", "upstream", 1)
	m.ObserveScaleUp("kept", "admit")

	if deleted := m.ForgetApp("gone"); deleted != 4 {
		t.Fatalf("ForgetApp(gone) deleted %d series, want 4 (two scale-up outcomes, one floor, one app_id circuit)", deleted)
	}
	if deleted := m.ForgetApp("gone"); deleted != 0 {
		t.Fatalf("second ForgetApp(gone) deleted %d series, want 0", deleted)
	}
	if deleted := m.ForgetApp("kept"); deleted != 1 {
		t.Fatalf("ForgetApp(kept) deleted %d series, want the one left", deleted)
	}
	var nilMetrics *wire.OpsMetrics
	if nilMetrics.ForgetApp("x") != 0 {
		t.Fatal("nil metrics deleted series")
	}
}

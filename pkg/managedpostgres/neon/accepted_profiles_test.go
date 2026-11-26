package neon

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Production moved from 1–4 to 0.5–4 CU over two releases
// (ADR-20261124221402): it provisions at 0.5 CU, and an endpoint created at
// 1 CU before still reads as production, not drift (spec_mismatch, a
// snapshot-copy conflict).
func TestProductionReadsAtBothRanges(t *testing.T) {
	for _, tt := range []struct {
		name     string
		min, max float64
		want     managedpostgres.ServiceClass
	}{
		{"production at 1 CU", 1, 4, managedpostgres.ClassProduction},
		{"production at 0.5 CU", 0.5, 4, managedpostgres.ClassProduction},
		{"development", 0.25, 1, managedpostgres.ClassDevelopment},
		{"burstable", 0.25, 2, managedpostgres.ClassBurstable},
		{"unknown range", 1, 2, ""},
	} {
		if got := classForSize(tt.min, tt.max); got != tt.want {
			t.Errorf("%s: classForSize(%v, %v) = %q, want %q", tt.name, tt.min, tt.max, got, tt.want)
		}
	}
	p := &Provider{}
	production := p.endpointSettingsForSpec(managedpostgres.Spec{Class: managedpostgres.ClassProduction, ScaleToZero: true})
	if production.MinimumCU != 0.5 || production.MaximumCU != 4 {
		t.Errorf("production provisions at %v–%v CU, want 0.5–4", production.MinimumCU, production.MaximumCU)
	}
	earlier := endpointSettings{MinimumCU: 1, MaximumCU: 4, SuspendTimeoutSecond: production.SuspendTimeoutSecond}
	if !sameEndpointSettings(earlier, production) {
		t.Error("a production endpoint created at 1 CU reads as drift")
	}
	burstable := p.endpointSettingsForSpec(managedpostgres.Spec{Class: managedpostgres.ClassBurstable, ScaleToZero: true})
	if sameEndpointSettings(production, burstable) || sameEndpointSettings(earlier, burstable) {
		t.Error("a production endpoint matches burstable")
	}
	// Sizes of no class compare exactly, as before.
	if !sameEndpointSettings(endpointSettings{MinimumCU: 1, MaximumCU: 2}, endpointSettings{MinimumCU: 1, MaximumCU: 2}) ||
		sameEndpointSettings(endpointSettings{MinimumCU: 1, MaximumCU: 2}, endpointSettings{MinimumCU: 1, MaximumCU: 3}) {
		t.Error("sizes of no class must match exactly")
	}
}

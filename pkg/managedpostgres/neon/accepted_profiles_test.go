package neon

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Production moves from 1–4 to 0.5–4 CU over two releases
// (ADR-20261124221402): every neond must read both ranges as production, so
// neither is drift (spec_mismatch, a snapshot-copy conflict) beside the other.
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
	if production.MinimumCU != 1 {
		t.Errorf("production provisions at %v CU; 0.5 waits for the next release", production.MinimumCU)
	}
	half := endpointSettings{MinimumCU: 0.5, MaximumCU: 4, SuspendTimeoutSecond: 300}
	if !sameEndpointSettings(half, production) {
		t.Error("a production endpoint at 0.5 CU reads as drift")
	}
	burstable := p.endpointSettingsForSpec(managedpostgres.Spec{Class: managedpostgres.ClassBurstable, ScaleToZero: true})
	if sameEndpointSettings(half, burstable) {
		t.Error("a production endpoint matches burstable")
	}
	// Sizes of no class compare exactly, as before.
	if !sameEndpointSettings(endpointSettings{MinimumCU: 1, MaximumCU: 2}, endpointSettings{MinimumCU: 1, MaximumCU: 2}) ||
		sameEndpointSettings(endpointSettings{MinimumCU: 1, MaximumCU: 2}, endpointSettings{MinimumCU: 1, MaximumCU: 3}) {
		t.Error("sizes of no class must match exactly")
	}
}

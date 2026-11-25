package neon

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// An endpoint an earlier release sized (production at 1–4 CU) keeps its class,
// so it is not drift: not spec_mismatch, not a snapshot-copy conflict.
func TestLegacyProductionRangeKeepsItsClass(t *testing.T) {
	for _, tt := range []struct {
		name     string
		min, max float64
		want     managedpostgres.ServiceClass
	}{
		{"current production", 0.5, 4, managedpostgres.ClassProduction},
		{"legacy production", 1, 4, managedpostgres.ClassProduction},
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
	legacy := endpointSettings{MinimumCU: 1, MaximumCU: 4, SuspendTimeoutSecond: 300}
	if !sameEndpointSettings(legacy, production) {
		t.Error("a legacy production endpoint reads as drift")
	}
	burstable := p.endpointSettingsForSpec(managedpostgres.Spec{Class: managedpostgres.ClassBurstable, ScaleToZero: true})
	if sameEndpointSettings(legacy, burstable) {
		t.Error("a legacy production endpoint matches burstable")
	}
	if sameEndpointSettings(endpointSettings{MinimumCU: 1, MaximumCU: 2}, endpointSettings{MinimumCU: 1, MaximumCU: 2}) {
		t.Error("an unknown range matches itself")
	}
}

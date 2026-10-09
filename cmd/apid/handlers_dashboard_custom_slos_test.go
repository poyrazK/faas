package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomSLOView(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	def := state.SLO{Name: "fast", SLI: state.SLILatency, LatencyThresholdMS: 250, ObjectiveBP: 9950, WindowDays: 7}
	tests := []struct {
		name      string
		status    api.SLOStatus
		wantClass string
		wantLeft  string
	}{
		{"healthy", api.SLOStatus{AttainmentPct: f(99.99), BudgetRemainingPct: f(98), BurnRate1h: f(0.1)}, "ok", "98.0%"},
		{"running low", api.SLOStatus{AttainmentPct: f(99.6), BudgetRemainingPct: f(20)}, "warn", "20.0%"},
		{"missed", api.SLOStatus{AttainmentPct: f(99), BudgetRemainingPct: f(-100)}, "bad", "-100.0%"},
		{"no traffic", api.SLOStatus{Source: "degraded: prometheus not configured"}, "dim", "—"},
	}
	for _, tt := range tests {
		v := customSLOView(def, &tt.status)
		if v.BudgetClass != tt.wantClass || v.BudgetRemaining != tt.wantLeft {
			t.Errorf("%s: class %q left %q, want %q %q", tt.name, v.BudgetClass, v.BudgetRemaining, tt.wantClass, tt.wantLeft)
		}
		if v.Objective != "99.5% under 250ms over 7d" {
			t.Errorf("%s: objective %q", tt.name, v.Objective)
		}
	}
}

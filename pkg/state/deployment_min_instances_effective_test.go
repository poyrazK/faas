package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

// TestDeployment_EffectiveMinInstances_Total mirrors
// TestApp_EffectiveMinInstances_Total: 7-branch table covering
// nil-safety, the inheritance default (0 = inherit), and the
// explicit-override range. ADR-072 §Decision 2 — the deployment
// column is the explicit override; the parent app's floor is the
// lower bound (max'd at the trigger, not in the helper — this
// helper returns the deployment's own contribution to that max).
func TestDeployment_EffectiveMinInstances_Total(t *testing.T) {
	cases := []struct {
		name string
		d    *state.Deployment
		want int
	}{
		{
			name: "nil receiver → 0 (safe default for inheritance walk)",
			d:    nil,
			want: 0,
		},
		{
			name: "zero (post-migration default) → 0 = inherit from parent app",
			d:    &state.Deployment{ID: "d1", AppID: "a1", MinInstances: 0},
			want: 0,
		},
		{
			name: "explicit override 1 → 1",
			d:    &state.Deployment{ID: "d2", AppID: "a2", MinInstances: 1},
			want: 1,
		},
		{
			name: "explicit override 5 → 5",
			d:    &state.Deployment{ID: "d3", AppID: "a3", MinInstances: 5},
			want: 5,
		},
		{
			name: "max per-plan cap (Scale = 10) → 10",
			d:    &state.Deployment{ID: "d4", AppID: "a4", MinInstances: 10},
			want: 10,
		},
		{
			name: "negative value → clamped to 0 (defensive; CHECK constraint blocks this in practice)",
			d:    &state.Deployment{ID: "d5", AppID: "a5", MinInstances: -1},
			want: 0,
		},
		{
			name: "struct copy (value semantics) preserves the override",
			d:    func() *state.Deployment { d := state.Deployment{ID: "d6", AppID: "a6", MinInstances: 3}; return &d }(),
			want: 3,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.d.EffectiveMinInstances()
			if got != c.want {
				t.Errorf("EffectiveMinInstances() = %d, want %d (d = %+v)", got, c.want, c.d)
			}
			// function-form parity
			if gotFn := state.EffectiveDeploymentMinInstances(c.d); gotFn != c.want {
				t.Errorf("effectiveDeploymentMinInstances() = %d, want %d", gotFn, c.want)
			}
		})
	}
}

// TestDeployment_EffectiveMinInstances_InheritsFromApp pins the
// inheritance rule. The helper returns only the deployment's own
// contribution; the parent's floor enters via max() at the trigger
// and reaper. A regression that conflated "inherit" with "set to
// 0" would short-circuit the per-deployment axis on every legacy
// deployment row (the post-migration default). This test pins the
// contract that callers compose max(app, deployment), not
// EffectiveMinInstances() in isolation.
func TestDeployment_EffectiveMinInstances_InheritsFromApp(t *testing.T) {
	app := &state.App{ID: "a1", MinInstances: 2}
	d := &state.Deployment{ID: "d1", AppID: "a1", MinInstances: 0}

	if d.EffectiveMinInstances() != 0 {
		t.Errorf("deployment contribution = %d, want 0 (inherit posture)", d.EffectiveMinInstances())
	}
	if app.EffectiveMinInstances() != 2 {
		t.Errorf("app floor = %d, want 2 (the lower bound)", app.EffectiveMinInstances())
	}
	// The trigger / reaper / meterd compose max(app, deployment):
	// the deployment's 0 contribution must NOT shadow the app's 2.
	maxFloor := app.EffectiveMinInstances()
	if dFloor := d.EffectiveMinInstances(); dFloor > maxFloor {
		maxFloor = dFloor
	}
	if maxFloor != 2 {
		t.Errorf("max(app, deployment) = %d, want 2 (deployment inheritance)", maxFloor)
	}
}

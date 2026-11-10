// adr: 20261101215228

package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// schedd sends vmmd no request limit until runtime config sets one, then the
// app's declaration up to the max, or the max.
func TestVMMRequestConcurrency(t *testing.T) {
	t.Cleanup(func() { api.SetRequestConcurrency(api.RequestConcurrency{}) })
	free := api.MustLimitsFor(api.PlanFree)
	plain := state.App{ID: "0b6f8c1e-2f6a-4c55-9d38-3c1f0c5e7a11"}
	declared := state.App{ID: plain.ID, ScalingPolicy: &state.ScalingPolicy{PerInstanceConcurrency: 300}}
	if got := sched.VMMRequestConcurrency(declared, free); got != 0 {
		t.Fatalf("unset: sent %d, want 0 (vmmd keeps the plan bound)", got)
	}
	api.SetRequestConcurrency(api.RequestConcurrency{Default: 80, Max: 1000})
	if got := sched.VMMRequestConcurrency(plain, free); got != 1000 {
		t.Errorf("no declaration: sent %d, want the max 1000", got)
	}
	if got := sched.VMMRequestConcurrency(declared, free); got != 300 {
		t.Errorf("declared 300: sent %d, want 300", got)
	}
}

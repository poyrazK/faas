package gateway

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A function's burst scale-out plans with its runner's execution slots, not
// the plan's 80 requests per VM, while admission keeps perVM
// (ADR-20261022203523 amendment 2026-11-19).
func TestBurstScaleOutConcurrency(t *testing.T) {
	scale := api.MustLimitsFor(api.PlanScale).PerInstanceConcurrency(0)
	cases := []struct {
		name string
		app  App
		want int
	}{
		{"plain app keeps the plan bound", App{}, scale},
		{"default function plans with four workers", App{FunctionRunnerSlots: api.FunctionRunnerSlots(0)}, 4},
		{"function_concurrency widens the runner", App{FunctionRunnerSlots: api.FunctionRunnerSlots(16)}, 64},
		{"a runner wider than the plan bound keeps the bound", App{FunctionRunnerSlots: api.FunctionRunnerSlots(32)}, scale},
		{"a declared per_instance_concurrency wins", App{PerInstanceConcurrency: 10, FunctionRunnerSlots: 4}, scale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := burstScaleOutConcurrency(tc.app, scale); got != tc.want {
				t.Fatalf("burstScaleOutConcurrency=%d, want %d", got, tc.want)
			}
		})
	}
}

// Ten requests in flight on a default function ask for three instances; the
// plan bound alone asked for one.
func TestBurstScaleOutFunctionBacklogWantsSiblings(t *testing.T) {
	scale := api.MustLimitsFor(api.PlanScale).PerInstanceConcurrency(0)
	app := App{FunctionRunnerSlots: api.FunctionRunnerSlots(0)}
	if got := desiredBurstInstances(10, burstScaleOutConcurrency(app, scale), 20); got != 3 {
		t.Fatalf("desired instances=%d, want 3", got)
	}
	if got := desiredBurstInstances(10, scale, 20); got != 1 {
		t.Fatalf("plan-bound desired instances=%d, want 1", got)
	}
}

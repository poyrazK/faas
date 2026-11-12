package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Floors and warm pools saved under a larger plan hold only what the current
// plan allows; an unresolved plan keeps the configured floor.
// adr: 20261105120000
func TestPlanCappedFloor(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plan  api.Plan
		floor int
		want  int
	}{
		{"scale floor on hobby", api.PlanHobby, 10, api.PlanHobby.MaxMinInstances()},
		{"scale floor on pro", api.PlanPro, 10, api.PlanPro.MaxMinInstances()},
		{"floor within plan", api.PlanScale, 3, 3},
		{"free has no floor", api.PlanFree, 3, 0},
		{"unresolved plan keeps floor", "", 4, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sched.PlanCappedFloor(tc.plan, tc.floor); got != tc.want {
				t.Fatalf("PlanCappedFloor(%q, %d) = %d, want %d", tc.plan, tc.floor, got, tc.want)
			}
		})
	}
}

// adr: 20261105120000
func TestWarmPoolTargetCappedByCurrentPlan(t *testing.T) {
	hobby := api.MustLimitsFor(api.PlanHobby).MaxConcurrency
	for _, tc := range []struct {
		name string
		app  state.App
		plan api.Plan
		want int
	}{
		{"scale pool on hobby", state.App{WarmPoolSize: 20, MaxConcurrency: 20}, api.PlanHobby, hobby},
		{"pool within plan", state.App{WarmPoolSize: 1}, api.PlanHobby, 1},
		{"app max below plan", state.App{WarmPoolSize: 5, MaxConcurrency: 2}, api.PlanScale, 2},
		{"negative pool", state.App{WarmPoolSize: -1}, api.PlanPro, 0},
		{"unknown plan", state.App{WarmPoolSize: 3}, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sched.WarmPoolTarget(tc.app, tc.plan); got != tc.want {
				t.Fatalf("WarmPoolTarget = %d, want %d", got, tc.want)
			}
		})
	}
}

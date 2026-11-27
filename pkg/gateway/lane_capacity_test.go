// adr: 590
package gateway

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// prod 2026-11-19: a warm staging instance of an app with max_concurrency 1
// made every production request answer 429 app_concurrency_reached ("1
// already live") until the stage instance idled out. Each lane counts only
// its own instances against its limit.
func TestLaneCapacityCountSeparatesProductionFromStages(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	b.appsPicker["app-1"] = &appPicker{
		weights: []deploymentWeight{{DeploymentID: "prod-new", Percent: 100}},
		sets: map[string]*targetSet{
			"stage": {entries: []Target{{NodeID: "node-a", InstanceID: "s1"}}},
		},
	}
	if got := b.LaneCapacityCount("app-1", ""); got != 0 {
		t.Fatalf("production lane counted %d instances, want 0 (the stage instance is not production's)", got)
	}
	if got := b.LaneCapacityCount("app-1", "prod-new"); got != 0 {
		t.Fatalf("exact production deployment counted %d, want 0", got)
	}
	if got := b.LaneCapacityCount("app-1", "stage"); got != 1 {
		t.Fatalf("stage lane counted %d, want 1", got)
	}
	if got := b.CapacityCount("app-1"); got != 1 {
		t.Fatalf("app-wide count=%d, want 1", got)
	}

	h := NewHandlerWith(b, NewMetrics(), nil)
	app := App{ID: "app-1", Plan: api.PlanScale, MaxConcurrency: 1}
	_, err := h.settleAtCapacity(t.Context(), app, "", api.MustLimitsFor(app.Plan), 1, func() PickResult { return PickResult{} })
	var problem *api.Problem
	if errors.As(err, &problem) && problem.Code == api.CodeAppConcurReached {
		t.Fatalf("a stage instance answered production 429 app_concurrency_reached: %v", err)
	}
	if !errors.Is(err, ErrInstanceSlotsSettling) {
		t.Fatalf("err=%v, want the retryable ErrInstanceSlotsSettling", err)
	}
}

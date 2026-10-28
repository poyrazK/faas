// adr: 20261018101500 — amendment 2026-10-27, worker floor from the scaling policy.
package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A worker declared with lifecycle and a scaling block takes its floor from
// min_instances, but only when the policy scales on a worker signal.
func TestWorkerPolicyFloor(t *testing.T) {
	queue := func(min int) *state.ScalingPolicy {
		return &state.ScalingPolicy{MinInstances: min, Target: &state.ScalingTarget{Metric: api.ScalingMetricQueueDepth, Value: 100}}
	}
	for name, tc := range map[string]struct {
		policy *state.ScalingPolicy
		floor  int
		ok     bool
	}{
		"no policy":           {nil, 0, false},
		"queue depth, min 0":  {queue(0), 0, true},
		"queue depth, min 2":  {queue(2), 2, true},
		"negative min":        {queue(-1), 0, true},
		"custom metric":       {&state.ScalingPolicy{Targets: []state.ScalingTarget{{Metric: api.ScalingMetricCustom, Name: "backlog", Value: 5}}}, 0, true},
		"cpu only keeps one":  {&state.ScalingPolicy{Target: &state.ScalingTarget{Metric: "cpu", Value: 70}}, 0, false},
		"no target keeps one": {&state.ScalingPolicy{MinInstances: 0}, 0, false},
	} {
		if floor, ok := workerPolicyFloor(tc.policy); floor != tc.floor || ok != tc.ok {
			t.Errorf("%s: workerPolicyFloor = %d, %v; want %d, %v", name, floor, ok, tc.floor, tc.ok)
		}
	}
}

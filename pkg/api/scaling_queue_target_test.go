package api

import (
	"net/http"
	"testing"
)

// A queue_depth or queue_lag value names the messages one worker takes; the
// scheduler counts the backlog up to value × max replicas each reconcile, so
// both target surfaces refuse more than ScalingQueueTargetMax with the limit
// and the observed value.
func TestQueueScalingTargetsAreBounded(t *testing.T) {
	for _, metric := range []string{ScalingMetricQueueDepth, ScalingMetricQueueLag} {
		at := ScalingTarget{Metric: metric, Value: ScalingQueueTargetMax}
		over := ScalingTarget{Metric: metric, Value: ScalingQueueTargetMax + 0.5}
		if p := ValidateScalingTargets("targets", []ScalingTarget{at}); p != nil {
			t.Errorf("%s at the maximum refused: %v", metric, p.Detail)
		}
		if p := ValidateLegacyScalingTarget(&at); p != nil {
			t.Errorf("legacy %s at the maximum refused: %v", metric, p.Detail)
		}
		for name, p := range map[string]*Problem{
			"targets": ValidateScalingTargets("targets", []ScalingTarget{over}),
			"target":  ValidateLegacyScalingTarget(&over),
		} {
			if p == nil || p.Status != http.StatusUnprocessableEntity || p.Limit == nil || *p.Limit != ScalingQueueTargetMax || p.Observed == nil || *p.Observed != ScalingQueueTargetMax+1 {
				t.Errorf("%s %s over the maximum = %+v, want 422 with limit and observed", name, metric, p)
			}
		}
	}
	if p := ValidateScalingTargets("targets", []ScalingTarget{{Metric: ScalingMetricRPS, Value: 2 * ScalingQueueTargetMax}}); p != nil {
		t.Errorf("an rps target above the queue maximum was refused: %v", p.Detail)
	}
}

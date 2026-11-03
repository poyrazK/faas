package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// The backlog count each reconcile makes stops at target × max replicas. A
// queue target stored before api.ScalingQueueTargetMax existed reads as that
// maximum, so one app's policy cannot make every pass count a whole queue.
func TestWorkerQueueDepthLimitCapsTheTarget(t *testing.T) {
	for _, tc := range []struct {
		target      float64
		maxReplicas int
		want        int
	}{
		{100, 3, 300},
		{api.ScalingQueueTargetMax, 2, 2 * api.ScalingQueueTargetMax},
		{1e9, 2, 2 * api.ScalingQueueTargetMax},
		{1e9, 0, api.ScalingQueueTargetMax},
	} {
		if got := sched.WorkerQueueDepthLimit(tc.target, tc.maxReplicas); got != tc.want {
			t.Errorf("WorkerQueueDepthLimit(%v, %d) = %d, want %d", tc.target, tc.maxReplicas, got, tc.want)
		}
	}
}

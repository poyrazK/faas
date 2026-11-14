// adr: 20261112193306
package targets

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Messages waiting behind their ordered key cannot run, so they earn no
// workers: one blocked key with a long tail asks for one worker, not many.
func TestQueueDemandLeavesOutMessagesWaitingBehindTheirKey(t *testing.T) {
	binding := state.QueueBinding{QueueName: "orders", Enabled: true, MaxConcurrency: 20}
	for _, tc := range []struct {
		name  string
		queue state.QueueStats
		want  int
	}{
		{"unordered backlog", state.QueueStats{Depth: 50}, 10},
		{"one key with a long tail", state.QueueStats{Depth: 50, WaitingBehindKey: 49, BlockedLanes: 1}, 1},
		{"every message waiting", state.QueueStats{Depth: 3, WaitingBehindKey: 3}, 0},
	} {
		signal := queueDepthSignal{bindings: []queueBindingDepth{{binding: binding, queue: tc.queue}}}
		if got := signal.bindingWorkerDemand(5)["orders"]; got != tc.want {
			t.Errorf("%s: demand %d, want %d", tc.name, got, tc.want)
		}
	}
	legacy := queueDepthSignal{queue: state.QueueStats{Depth: 50, WaitingBehindKey: 45}}
	if got := legacy.desiredWorkers(5, 20); got != 1 {
		t.Errorf("app-wide signal: %d workers, want 1", got)
	}
}

package wire_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestOpsMetrics_QueueState(t *testing.T) {
	m := wire.NewOpsMetrics("schedd")
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	m.SetQueueState("worker-1", 7, 2, 3, now.Add(-5*time.Second), now)
	m.SetQueueState("worker-empty", -1, -1, -1, time.Time{}, now)
	m.SetQueueBindingState("worker-1", "orders", 4, 1, 2, now.Add(-3*time.Second), now)
	m.SetQueueBindingWorkerDemand("worker-1", "orders", 1)
	m.ObserveQueueBindingConcurrencyThrottled("worker-1", "orders")
	body := render(t, m)
	for _, want := range []string{
		`schedd_queue_depth{app="worker-1"} 7`,
		`schedd_queue_in_flight{app="worker-1"} 2`,
		`schedd_queue_oldest_age_seconds{app="worker-1"} 5`,
		`schedd_queue_dead_letter{app="worker-1"} 3`,
		`schedd_queue_depth{app="worker-empty"} 0`,
		`schedd_queue_oldest_age_seconds{app="worker-empty"} 0`,
		`schedd_queue_binding_depth{app="worker-1",binding="orders"} 4`,
		`schedd_queue_binding_in_flight{app="worker-1",binding="orders"} 1`,
		`schedd_queue_binding_lag_seconds{app="worker-1",binding="orders"} 3`,
		`schedd_queue_binding_dead_letter{app="worker-1",binding="orders"} 2`,
		`schedd_queue_binding_worker_demand{app="worker-1",binding="orders"} 1`,
		`schedd_queue_binding_concurrency_throttled_total{app="worker-1",binding="orders"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing queue metric %q in:\n%s", want, body)
		}
	}
}

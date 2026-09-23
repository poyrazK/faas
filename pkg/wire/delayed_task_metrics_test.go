package wire_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestOpsMetrics_DelayedTaskDispatch(t *testing.T) {
	m := wire.NewOpsMetrics("schedd")
	m.ObserveDelayedTaskDispatch(wire.DelayedTaskDispatchSuccess)
	m.ObserveDelayedTaskDispatch(wire.DelayedTaskDispatchRetry)
	m.ObserveDelayedTaskDispatch(wire.DelayedTaskDispatchDeadLetter)
	m.ObserveDelayedTaskDispatch("unbounded-error-text")
	m.ObserveDelayedTaskScheduleLag(7 * time.Second)
	m.ObserveDelayedTaskScheduleLag(-time.Second)

	body := render(t, m)
	for _, want := range []string{
		`schedd_delayed_task_dispatch_total{outcome="success"} 1`,
		`schedd_delayed_task_dispatch_total{outcome="retry"} 1`,
		`schedd_delayed_task_dispatch_total{outcome="failed"} 0`,
		`schedd_delayed_task_dispatch_total{outcome="dead_letter"} 1`,
		`schedd_delayed_task_schedule_lag_seconds_bucket{le="10"} 1`,
		`schedd_delayed_task_schedule_lag_seconds_count 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing delayed-task metric %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "unbounded-error-text") {
		t.Fatalf("unknown outcome escaped the closed label vocabulary:\n%s", body)
	}
}

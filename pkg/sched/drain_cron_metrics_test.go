package sched_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A plain HTTP cron run that fails ends failed on its single attempt; one
// that succeeds counts as success. Both record first-claim lag from the
// scheduled time.
func TestDrain_ObservesCronLagAndOutcomes(t *testing.T) {
	ctx := context.Background()
	d, store, _, _, synth := sched.NewDrainHarness(t, api.PlanHobby, true)
	now := time.Now().UTC().Truncate(time.Second)
	*d.Now() = func() time.Time { return now }
	metrics := wire.NewOpsMetrics("schedd")
	d.XObserveWith(metrics)

	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) == 0 {
		t.Fatalf("ListAllApps: %v / %d apps", err, len(apps))
	}
	enqueue := func(scheduledAt time.Time) {
		t.Helper()
		if _, err := store.EnqueueInvocation(ctx, state.Invocation{
			AppID: apps[0].ID, AccountID: apps[0].AccountID,
			Source: state.InvocationCron, Method: http.MethodPost, Path: "/tick",
			DueAt: scheduledAt, ScheduledAt: &scheduledAt,
			RetryPolicyJSON: json.RawMessage(`{"max_attempts":1}`),
		}); err != nil {
			t.Fatalf("EnqueueInvocation: %v", err)
		}
	}

	enqueue(now.Add(-20 * time.Second))
	synth.Transient().Store(true)
	d.Tick(ctx)
	synth.Transient().Store(false)
	enqueue(now.Add(-3 * time.Second))
	d.Tick(ctx)

	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`schedd_cron_delivery_total{outcome="failed"} 1`,
		`schedd_cron_delivery_total{outcome="success"} 1`,
		`schedd_cron_delivery_total{outcome="retry"} 0`,
		`schedd_cron_schedule_lag_seconds_bucket{le="2.5"} 0`,
		`schedd_cron_schedule_lag_seconds_bucket{le="30"} 2`,
		`schedd_cron_schedule_lag_seconds_count 2`,
		`schedd_delayed_task_dispatch_total{outcome="success"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

package main_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type queueStatusFakeClient struct {
	app      api.AppResponse
	queue    api.QueueStateResponse
	bindings []api.QueueBindingResponse
	statuses map[string]api.QueueBindingStatusResponse
}

func (f *queueStatusFakeClient) GetApp(context.Context, string) (api.AppResponse, error) {
	return f.app, nil
}

func (f *queueStatusFakeClient) QueueState(context.Context, string) (api.QueueStateResponse, error) {
	return f.queue, nil
}

func (f *queueStatusFakeClient) ListQueueBindings(context.Context, string) ([]api.QueueBindingResponse, error) {
	return f.bindings, nil
}

func (f *queueStatusFakeClient) GetQueueBindingStatus(_ context.Context, _, id string) (api.QueueBindingStatusResponse, error) {
	status, ok := f.statuses[id]
	if !ok {
		return api.QueueBindingStatusResponse{}, errors.New("missing status")
	}
	return status, nil
}

func TestCollectQueueStatusIncludesScalingAndBindingHealth(t *testing.T) {
	fake := &queueStatusFakeClient{
		app: api.AppResponse{Slug: "demo", ScalingPolicy: &api.ScalingPolicy{
			MinInstances: 0,
			Target:       &api.ScalingTarget{Metric: "queue_depth", Value: 10},
		}},
		queue:    api.QueueStateResponse{AppSlug: "demo", Plan: "pro", PlanCap: 100, Depth: 12, InFlight: 2},
		bindings: []api.QueueBindingResponse{{ID: "binding-1", Name: "default", QueueName: "default", Mode: "push", WorkloadClass: "worker", Enabled: true}},
		statuses: map[string]api.QueueBindingStatusResponse{
			"binding-1": {BindingID: "binding-1", Name: "default", ConsumerState: "active", ConsumerLiveness: "healthy", Depth: 12, InFlight: 2},
		},
	}

	report, err := main.CollectQueueStatus(context.Background(), fake, "demo")
	if err != nil {
		t.Fatalf("collectQueueStatus() error = %v", err)
	}
	if report.Health != "healthy" {
		t.Fatalf("health = %q, want healthy", report.Health)
	}
	if report.Queue.Depth != 12 || report.ScalingPolicy == nil || report.ScalingPolicy.Target == nil {
		t.Fatalf("report = %+v, want queue and scaling details", report)
	}
	if len(report.Bindings) != 1 || report.Bindings[0].ConsumerLiveness != "healthy" {
		t.Fatalf("bindings = %+v, want one healthy binding", report.Bindings)
	}
}

func TestQueueStatusHealthWorstBindingWins(t *testing.T) {
	statuses := []api.QueueBindingStatusResponse{
		{ConsumerLiveness: "healthy"},
		{ConsumerLiveness: "degraded"},
		{ConsumerLiveness: "stale"},
	}
	if got := main.QueueStatusHealth(statuses); got != "stale" {
		t.Fatalf("queueStatusHealth() = %q, want stale", got)
	}
	if got := main.QueueStatusHealth(nil); got != "not_configured" {
		t.Fatalf("queueStatusHealth(nil) = %q, want not_configured", got)
	}
}

func TestRenderQueueStatusIncludesActionableSignals(t *testing.T) {
	oldOut := (*main.OsStdout)
	t.Cleanup(func() { (*main.OsStdout) = oldOut })
	var out bytes.Buffer
	(*main.OsStdout) = &out
	oldestAge := int64(42)
	lag := int64(7)
	lagAge := 3.5
	report := main.QueueStatusReport{
		AppSlug: "demo", Health: "degraded",
		Queue:         api.QueueStateResponse{Depth: 12, InFlight: 2, PlanCap: 100, OldestPendingAgeSeconds: &oldestAge},
		ScalingPolicy: &api.ScalingPolicy{Target: &api.ScalingTarget{Metric: "queue_depth", Value: 10}},
		Bindings:      []api.QueueBindingStatusResponse{{Name: "default", QueueName: "default", Mode: "push", WorkloadClass: "worker", Enabled: true, ConsumerState: "active", ConsumerLiveness: "degraded", LagMessages: &lag, LagAgeSeconds: &lagAge, DeadLetter: 3, LastError: "worker timeout"}},
	}
	main.RenderQueueStatus(report)
	for _, want := range []string{"health:     degraded", "depth=12", "oldest_age: 42s", "target=queue_depth:10", "lag=7", "lag_age=3.5s", "dead_letter=3", "last_error: worker timeout"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("rendered status missing %q: %s", want, out.String())
		}
	}
}

// A scaling policy's unset bounds read as the app's own: "min=0 max=0"
// described a consumer that can run max_concurrency instances (H10-75).
func TestQueueScalingBoundsUseAppDefaults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report main.QueueStatusReport
		want   string
	}{
		{"unset policy", main.QueueStatusReport{ScalingPolicy: &api.ScalingPolicy{}, MaxConcurrency: 5}, "min=0 max=5 (max_concurrency)"},
		{"no policy", main.QueueStatusReport{MinInstances: 1, MaxConcurrency: 3}, "min=1 max=3 (max_concurrency)"},
		{"explicit", main.QueueStatusReport{ScalingPolicy: &api.ScalingPolicy{MinInstances: 2, MaxInstances: 4}, MaxConcurrency: 5}, "min=2 max=4"},
	} {
		if got := main.QueueScalingBounds(tc.report); got != tc.want {
			t.Errorf("%s: bounds = %q, want %q", tc.name, got, tc.want)
		}
	}
}

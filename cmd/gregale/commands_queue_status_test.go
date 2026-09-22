package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

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

	report, err := collectQueueStatus(context.Background(), fake, "demo")
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
	if got := queueStatusHealth(statuses); got != "stale" {
		t.Fatalf("queueStatusHealth() = %q, want stale", got)
	}
	if got := queueStatusHealth(nil); got != "not_configured" {
		t.Fatalf("queueStatusHealth(nil) = %q, want not_configured", got)
	}
}

func TestRenderQueueStatusIncludesActionableSignals(t *testing.T) {
	oldOut := osStdout
	t.Cleanup(func() { osStdout = oldOut })
	var out bytes.Buffer
	osStdout = &out
	oldestAge := int64(42)
	lag := int64(7)
	lagAge := 3.5
	report := queueStatusReport{
		AppSlug: "demo", Health: "degraded",
		Queue:         api.QueueStateResponse{Depth: 12, InFlight: 2, PlanCap: 100, OldestPendingAgeSeconds: &oldestAge},
		ScalingPolicy: &api.ScalingPolicy{Target: &api.ScalingTarget{Metric: "queue_depth", Value: 10}},
		Bindings:      []api.QueueBindingStatusResponse{{Name: "default", QueueName: "default", Mode: "push", WorkloadClass: "worker", Enabled: true, ConsumerState: "active", ConsumerLiveness: "degraded", LagMessages: &lag, LagAgeSeconds: &lagAge, DeadLetter: 3, LastError: "worker timeout"}},
	}
	renderQueueStatus(report)
	for _, want := range []string{"health:     degraded", "depth=12", "oldest_age: 42s", "target=queue_depth:10", "lag=7", "lag_age=3.5s", "dead_letter=3", "last_error: worker timeout"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("rendered status missing %q: %s", want, out.String())
		}
	}
}

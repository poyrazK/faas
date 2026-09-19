package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestConfigureQueueWorkloadCreatesAndConverges(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{
		AccountID: e.acct.ID, Slug: "queue-profile", WorkloadClass: state.WorkloadClassWorker,
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, http.MethodPut, "/v1/apps/queue-profile/queue-workload", api.QueueWorkloadProfileRequest{}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var got api.QueueWorkloadProfileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if got.Binding.Name != "default" || got.Binding.QueueName != "default" || got.Binding.Mode != "push" || got.Binding.WorkloadClass != "worker" || !got.Binding.Enabled || got.Binding.MaxConcurrency != 1 {
		t.Fatalf("binding = %+v", got.Binding)
	}
	if got.ScalingPolicy == nil || got.ScalingPolicy.Target == nil || got.ScalingPolicy.Target.Metric != "queue_depth" || got.ScalingPolicy.Target.Value != 10 {
		t.Fatalf("scaling policy = %+v", got.ScalingPolicy)
	}
	if got.App.Slug != app.Slug || got.App.WorkloadClass != "worker" {
		t.Fatalf("app response = %+v", got.App)
	}
	triggerID, err := queueBindingTriggerID(ctx, e.store, app.ID, got.Binding.ID)
	if err != nil || triggerID == "" {
		t.Fatalf("consumer projection = %q, err=%v", triggerID, err)
	}

	stored, err := e.store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ScalingPolicy == nil || stored.ScalingPolicy.Target == nil || stored.ScalingPolicy.Target.Metric != "queue_depth" {
		t.Fatalf("stored scaling policy = %+v", stored.ScalingPolicy)
	}

	rec = e.do(t, http.MethodPut, "/v1/apps/queue-profile/queue-workload", api.QueueWorkloadProfileRequest{
		QueueName: "orders", TargetDepth: 5, MaxConcurrency: 3, Force: true,
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("converge status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got = api.QueueWorkloadProfileResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode converge response: %v", err)
	}
	if got.Created || got.Binding.QueueName != "orders" || got.Binding.MaxConcurrency != 3 || got.ScalingPolicy == nil || got.ScalingPolicy.Target == nil || got.ScalingPolicy.Target.Value != 5 {
		t.Fatalf("converged profile = %+v", got)
	}
	bindings, err := e.store.ListQueueBindingsForApp(ctx, e.acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].QueueName != "orders" {
		t.Fatalf("bindings after converge = %+v", bindings)
	}
}

func TestConfigureQueueWorkloadRejectsQueueMoveWithoutForce(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{
		AccountID: e.acct.ID, Slug: "queue-conflict", WorkloadClass: state.WorkloadClassWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateQueueBinding(ctx, state.QueueBinding{
		AccountID: e.acct.ID, AppID: app.ID, Name: "default", QueueName: "payments",
		Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1,
	}); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, http.MethodPut, "/v1/apps/queue-conflict/queue-workload", api.QueueWorkloadProfileRequest{QueueName: "orders"}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	stored, err := e.store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ScalingPolicy != nil {
		t.Fatalf("scaling policy changed on conflict: %+v", stored.ScalingPolicy)
	}
}

func TestConfigureQueueWorkloadIsPlanGated(t *testing.T) {
	e := setup(t, api.PlanFree)
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID, Slug: "queue-free", WorkloadClass: state.WorkloadClassWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodPut, "/v1/apps/queue-free/queue-workload", api.QueueWorkloadProfileRequest{}, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402; body=%s", rec.Code, rec.Body.String())
	}
	bindings, err := e.store.ListQueueBindingsForApp(context.Background(), e.acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 0 {
		t.Fatalf("free-plan bindings = %+v, want none", bindings)
	}
}

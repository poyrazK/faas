package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQueueBindingStatusReportsProjectionAndQueueState(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{
		AccountID: e.acct.ID, Slug: "queue-status", WorkloadClass: state.WorkloadClassWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := e.store.CreateQueueBinding(ctx, state.QueueBinding{
		AccountID: e.acct.ID, AppID: app.ID, ID: "binding-status", Name: "orders",
		QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker,
		Enabled: true, MaxConcurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.syncQueueBindingConsumer(ctx, app, e.acct, binding); err != nil {
		t.Fatalf("create projection: %v", err)
	}
	if _, err := e.store.EnqueueInvocation(ctx, state.Invocation{
		AccountID: e.acct.ID, AppID: app.ID, QueueName: "orders", Source: state.InvocationQueue,
		Payload: []byte(`{"order":"123"}`), DueAt: time.Now().UTC().Add(-time.Minute),
		CreatedAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed queue row: %v", err)
	}

	rec := e.do(t, http.MethodGet, "/v1/apps/queue-status/queue-bindings/binding-status/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got api.QueueBindingStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ConsumerState != "active" || got.ConsumerStateReason != "push_consumer_enabled" {
		t.Fatalf("consumer state = %q/%q, want active/push_consumer_enabled", got.ConsumerState, got.ConsumerStateReason)
	}
	if got.TriggerID == "" {
		t.Fatal("trigger_id is empty for a projected push consumer")
	}
	if got.Depth != 1 || got.InFlight != 0 || got.DeadLetter != 0 {
		t.Fatalf("queue counters = depth=%d in_flight=%d dead_letter=%d, want 1/0/0", got.Depth, got.InFlight, got.DeadLetter)
	}
	if got.OldestPendingAt == nil || got.OldestPendingAgeSeconds == nil {
		t.Fatal("oldest pending fields are missing")
	}

	disabled := false
	binding, err = e.store.UpdateQueueBinding(ctx, e.acct.ID, app.ID, binding.ID, state.UpdateQueueBindingParams{Enabled: &disabled})
	if err != nil {
		t.Fatalf("disable binding: %v", err)
	}
	if err := e.s.syncQueueBindingConsumer(ctx, app, e.acct, binding); err != nil {
		t.Fatalf("update projection: %v", err)
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/queue-status/queue-bindings/binding-status/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got = api.QueueBindingStatusResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode disabled status: %v", err)
	}
	if got.ConsumerState != "paused" || got.ConsumerStateReason != "push_consumer_disabled" {
		t.Fatalf("disabled consumer state = %q/%q, want paused/push_consumer_disabled", got.ConsumerState, got.ConsumerStateReason)
	}

	pull := "pull"
	binding, err = e.store.UpdateQueueBinding(ctx, e.acct.ID, app.ID, binding.ID, state.UpdateQueueBindingParams{Mode: &pull})
	if err != nil {
		t.Fatalf("switch binding to pull: %v", err)
	}
	if err := e.s.syncQueueBindingConsumer(ctx, app, e.acct, binding); err != nil {
		t.Fatalf("remove projection: %v", err)
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/queue-status/queue-bindings/binding-status/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("pull status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got = api.QueueBindingStatusResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode pull status: %v", err)
	}
	if got.ConsumerState != "external" || got.ConsumerStateReason != "" || got.TriggerID != "" {
		t.Fatalf("pull consumer state = %q/%q trigger=%q, want external/no reason/no trigger", got.ConsumerState, got.ConsumerStateReason, got.TriggerID)
	}
}

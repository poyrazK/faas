package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQueueBindingConsumerLifecycle(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "queue-worker", WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := e.store.CreateQueueBinding(ctx, state.QueueBinding{
		AccountID: e.acct.ID, AppID: app.ID, ID: "binding-lifecycle", Name: "orders",
		QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker,
		Enabled: true, MaxConcurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.syncQueueBindingConsumer(ctx, app, e.acct, binding); err != nil {
		t.Fatalf("create projection: %v", err)
	}
	triggers, err := e.store.ListTriggersForApp(ctx, app.ID)
	if err != nil || len(triggers) != 1 {
		t.Fatalf("triggers after create = %d, err=%v", len(triggers), err)
	}
	if triggers[0].Slug != "orders" || !triggers[0].Enabled || triggers[0].Source.String != string(state.InvocationQueue) {
		t.Fatalf("unexpected queue consumer: %+v", triggers[0])
	}

	binding.QueueName = "payments"
	binding.Enabled = false
	if err := e.s.syncQueueBindingConsumer(ctx, app, e.acct, binding); err != nil {
		t.Fatalf("update projection: %v", err)
	}
	triggers, err = e.store.ListTriggersForApp(ctx, app.ID)
	if err != nil || len(triggers) != 1 {
		t.Fatalf("triggers after update = %d, err=%v", len(triggers), err)
	}
	if triggers[0].Slug != "payments" || triggers[0].Enabled {
		t.Fatalf("updated queue consumer = %+v", triggers[0])
	}

	binding.Mode = "pull"
	if err := e.s.syncQueueBindingConsumer(ctx, app, e.acct, binding); err != nil {
		t.Fatalf("delete projection: %v", err)
	}
	triggers, err = e.store.ListTriggersForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 0 {
		t.Fatalf("triggers after pull transition = %d, want 0", len(triggers))
	}
}

func TestQueueBindingTriggerConfigCarriesRetryPolicy(t *testing.T) {
	binding := state.QueueBinding{
		ID:              "binding-1",
		RetryPolicyJSON: json.RawMessage(`{"max_attempts":4,"base_seconds":2,"max_seconds":30,"jitter_seconds":0.2}`),
	}
	var got map[string]any
	if err := json.Unmarshal(queueBindingTriggerConfig(binding), &got); err != nil {
		t.Fatalf("queueBindingTriggerConfig: %v", err)
	}
	if got["mode"] != "queue" || got["queue_binding_id"] != "binding-1" {
		t.Fatalf("config markers = %#v", got)
	}
	policy, ok := got["retry_policy"].(map[string]any)
	if !ok || policy["max_attempts"] != float64(4) || policy["base_seconds"] != float64(2) {
		t.Fatalf("retry policy = %#v", got["retry_policy"])
	}
}

func TestQueueBindingTriggerDeliveryCaps(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanHobby)
	binding := state.QueueBinding{MaxConcurrency: 5000, RetryPolicyJSON: json.RawMessage(`{"max_attempts":25}`)}
	if got := queueBindingTriggerBatch(binding, limits); got != int32(limits.TriggerBatchSizeMax) {
		t.Fatalf("batch = %d, want plan cap %d", got, limits.TriggerBatchSizeMax)
	}
	if got := queueBindingTriggerAttempts(binding, limits); got != int32(limits.TriggerMaxAttemptsMax) {
		t.Fatalf("attempts = %d, want plan cap %d", got, limits.TriggerMaxAttemptsMax)
	}
}

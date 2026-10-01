package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 231 — an HTTP function can consume push deliveries, but an HTTP app
// or pull binding cannot borrow the function-only trigger contract.
func TestHTTPFunctionPushQueueBinding(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID, Slug: "queue-function", Type: state.AppTypeFunction,
		Runtime: "node22", WorkloadClass: state.WorkloadClassHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	create := api.CreateQueueBindingRequest{
		Name: "default", QueueName: "default", Mode: "push",
		WorkloadClass: "http", MaxConcurrency: 1,
	}
	rec := e.do(t, http.MethodPost, "/v1/apps/queue-function/queue-bindings", create, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	var binding api.QueueBindingResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &binding); err != nil {
		t.Fatal(err)
	}
	if binding.WorkloadClass != "http" || binding.Mode != "push" {
		t.Fatalf("created binding = %+v", binding)
	}
	triggers, err := e.store.ListTriggersForApp(context.Background(), app.ID)
	if err != nil || len(triggers) != 1 {
		t.Fatalf("push trigger count = %d, err = %v", len(triggers), err)
	}
	pull := "pull"
	rec = e.do(t, http.MethodPatch, "/v1/apps/queue-function/queue-bindings/"+binding.ID,
		api.UpdateQueueBindingRequest{Mode: &pull}, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	create.Name, create.QueueName, create.Mode = "pull", "pull", "pull"
	rec = e.do(t, http.MethodPost, "/v1/apps/queue-function/queue-bindings", create, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)

	_, err = e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID, Slug: "queue-http-app", Type: state.AppTypeApp,
		WorkloadClass: state.WorkloadClassHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	create.Mode = "push"
	rec = e.do(t, http.MethodPost, "/v1/apps/queue-http-app/queue-bindings", create, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}

func TestQueueBindingConsumerLifecycle(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "queue-worker", WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{
		AccountID: e.acct.ID, AppID: app.ID, Name: "orders",
		QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker,
		Enabled: true, MaxConcurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := result.Binding
	triggers, err := e.store.ListTriggersForApp(ctx, app.ID)
	if err != nil || len(triggers) != 1 {
		t.Fatalf("triggers after create = %d, err=%v", len(triggers), err)
	}
	if triggers[0].Slug != "orders" || !triggers[0].Enabled || triggers[0].Source.String != string(state.InvocationQueue) {
		t.Fatalf("unexpected queue consumer: %+v", triggers[0])
	}

	binding.QueueName = "payments"
	binding.Enabled = false
	if _, err := e.store.UpdateQueueBindingWithConsumer(ctx, e.acct.ID, app.ID, binding.ID, state.UpdateQueueBindingParams{QueueName: &binding.QueueName, Enabled: &binding.Enabled}); err != nil {
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
	if _, err := e.store.UpdateQueueBindingWithConsumer(ctx, e.acct.ID, app.ID, binding.ID, state.UpdateQueueBindingParams{Mode: &binding.Mode}); err != nil {
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

func TestQueueBindingHTTPConflictPreservesConsumer(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "queue-http-conflict", WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodPost, "/v1/apps/queue-http-conflict/queue-bindings", api.CreateQueueBindingRequest{
		Name: "jobs", QueueName: "jobs", Mode: "push", WorkloadClass: "worker", MaxConcurrency: 1,
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	var binding api.QueueBindingResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &binding); err != nil {
		t.Fatal(err)
	}
	triggerID, err := queueBindingTriggerID(ctx, e.store, app.ID, binding.ID)
	if err != nil || triggerID == "" {
		t.Fatalf("consumer identity = %q, err=%v", triggerID, err)
	}
	receiptID, err := e.store.InsertTriggerRecord(ctx, triggerID, "receipt", []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateTriggerIfUnderQuota(ctx, app.ID, "nats", "reserved", false, []byte(`{}`), "", 1, 1000, 3, 1024, "commit", api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	reserved := "reserved"
	rec = e.do(t, http.MethodPatch, "/v1/apps/queue-http-conflict/queue-bindings/"+binding.ID, api.UpdateQueueBindingRequest{QueueName: &reserved}, nil)
	assertProblem(t, rec, http.StatusConflict, api.CodeValidation)
	stored, err := e.store.QueueBindingByID(ctx, e.acct.ID, app.ID, binding.ID)
	if err != nil || stored.QueueName != "jobs" {
		t.Fatalf("failed update changed binding: queue=%q err=%v", stored.QueueName, err)
	}
	trigger, err := e.store.TriggerByID(ctx, triggerID)
	if err != nil || trigger.Slug != "jobs" {
		t.Fatalf("failed update changed consumer: slug=%q err=%v", trigger.Slug, err)
	}
	if id, err := e.store.TriggerRecordIDByItemIdentifier(ctx, triggerID, "receipt"); err != nil || id != receiptID {
		t.Fatalf("failed update lost receipt: id=%q err=%v", id, err)
	}
}

func TestQueueBindingHTTPQuotaFailureLeavesNoBinding(t *testing.T) {
	e := setup(t, api.PlanHobby)
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "queue-http-quota", WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanHobby)
	for i := 0; i < limits.TriggerLimitPerApp; i++ {
		if _, err := e.store.CreateTriggerIfUnderQuota(ctx, app.ID, "nats", fmt.Sprintf("reserved-%d", i), false, []byte(`{}`), "", 1, 1000, 3, 1024, "commit", limits); err != nil {
			t.Fatal(err)
		}
	}
	enabled := false
	rec := e.do(t, http.MethodPost, "/v1/apps/queue-http-quota/queue-bindings", api.CreateQueueBindingRequest{
		Name: "jobs", QueueName: "jobs", Mode: "push", WorkloadClass: "worker", Enabled: &enabled, MaxConcurrency: 1,
	}, nil)
	assertProblem(t, rec, http.StatusForbidden, api.CodePlanTriggerQuota)
	bindings, err := e.store.ListQueueBindingsForApp(ctx, e.acct.ID, app.ID)
	if err != nil || len(bindings) != 0 {
		t.Fatalf("quota failure left bindings=%d err=%v", len(bindings), err)
	}
	triggers, err := e.store.ListTriggersForApp(ctx, app.ID)
	if err != nil || len(triggers) != limits.TriggerLimitPerApp {
		t.Fatalf("quota failure changed trigger count=%d err=%v", len(triggers), err)
	}
}

func TestQueueBindingConsumerRejectsDirectTriggerMutation(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "queue-private-consumer", WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: e.acct.ID, AppID: app.ID,
		Name: "jobs", QueueName: "jobs", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	id := result.Changes[0].TriggerID
	enabled := false
	for _, tc := range []struct {
		name, method, suffix string
		body                 any
	}{
		{"patch", http.MethodPatch, "", api.UpdateTriggerRequest{Enabled: &enabled}},
		{"delete", http.MethodDelete, "", nil},
		{"pause", http.MethodPost, "/pause", nil},
		{"resume", http.MethodPost, "/resume", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.do(t, tc.method, "/v1/triggers/"+id+tc.suffix, tc.body, nil)
			assertProblem(t, rec, http.StatusConflict, api.CodeValidation)
		})
	}
	trigger, err := e.store.TriggerByID(ctx, id)
	if err != nil || !trigger.Enabled || !trigger.QueueBindingID.Valid {
		t.Fatalf("private consumer changed: enabled=%v err=%v", trigger.Enabled, err)
	}
	// The ordinary trigger endpoint cannot impersonate an owned projection.
	rec := e.do(t, http.MethodPost, "/v1/triggers", api.CreateTriggerRequest{AppID: app.ID,
		Kind: api.TriggerKindQueue, Slug: "forged", Config: json.RawMessage(`{"mode":"queue","queue_binding_id":null}`)}, nil)
	assertProblem(t, rec, http.StatusUnprocessableEntity, "trigger_invalid_config")
}

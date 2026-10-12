package apidserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReplayInvocation_BoundQueueRetainsDeliveryIdentity(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := t.Context()
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "bound-replay", Type: state.AppTypeFunction, WorkloadClass: state.WorkloadClassHTTP})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := (*e.Store()).CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: (*e.Acct()).ID, AppID: app.ID, Name: "original", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassHTTP, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	original, err := (*e.Store()).EnqueueInvocation(ctx, state.Invocation{AccountID: (*e.Acct()).ID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "orders", DeploymentScope: "staging", Payload: []byte(`{}`), DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := forceInvocationState(t, e, original.ID, "dead_letter"); err != nil {
		t.Fatal(err)
	}
	name := "payments"
	if _, err := (*e.Store()).UpdateQueueBindingWithConsumer(ctx, (*e.Acct()).ID, app.ID, binding.Binding.ID, state.UpdateQueueBindingParams{QueueName: &name}); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodPost, "/v1/invocations/"+original.ID+"/replay", nil, nil)
	var problem api.Problem
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != "queue_replay_requires_binding" {
		t.Fatalf("generic replay orphaned queue identity: %d %s", rec.Code, rec.Body.String())
	}
	if row, err := (*e.Store()).InvocationByID(ctx, original.ID); err != nil || row.State != state.InvocationDeadLetter {
		t.Fatalf("rejected replay changed ledger: %+v %v", row, err)
	}
	if err := (*e.Store()).DeleteQueueBinding(ctx, (*e.Acct()).ID, app.ID, binding.Binding.ID); err != nil {
		t.Fatal(err)
	}
	rec = e.Do(t, http.MethodPost, "/v1/apps/bound-replay/queues/dead_letter/"+original.ID+"/replay", nil, nil)
	var response api.AsyncInvokeResponse
	if rec.Code != http.StatusAccepted || json.Unmarshal(rec.Body.Bytes(), &response) != nil || response.ID != original.ID {
		t.Fatalf("durable replay replaced ledger: %d %s", rec.Code, rec.Body.String())
	}
	row, err := (*e.Store()).InvocationByID(ctx, original.ID)
	if err != nil || row.QueueBindingID != binding.Binding.ID || row.QueueName != "orders" || row.DeploymentScope != "staging" || row.State != state.InvocationPending {
		t.Fatalf("durable replay changed identity: %+v %v", row, err)
	}
	if _, err := (*e.Store()).ClaimInvocationWithCap(ctx, original.ID, "", 60, 10); !errors.Is(err, state.ErrQueueBindingRetired) {
		t.Fatalf("durable replay escaped retirement: %v", err)
	}
}

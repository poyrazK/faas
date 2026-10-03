package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowCallbackWebhookBindingCompletesOnlyVerifiedMatch(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	app := seedWorkflowApp(t, e, "bound-callback")
	endpoint := mustCreateInboundWebhook(t, e, app.Slug)
	create := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/workflows/callback/runs", map[string]any{"request_id": "bound-1"}, nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("create run: %d %s", create.Code, create.Body.String())
	}
	var run api.WorkflowRunResponse
	if err := json.Unmarshal(create.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	callbackID := api.WorkflowCallbackID(run.ID, "await")
	path := fmt.Sprintf("/v1/workflows/runs/%s/callbacks/%s/webhook-binding", run.ID, callbackID)
	request := api.CreateWorkflowCallbackWebhookBindingRequest{
		EndpointID: endpoint.ID, EventType: "payment_intent.succeeded", ObjectID: "pi_bound_1",
	}
	put := e.do(t, http.MethodPut, path, request, nil)
	if put.Code != http.StatusOK {
		t.Fatalf("put binding: %d %s", put.Code, put.Body.String())
	}
	var binding api.WorkflowCallbackWebhookBindingResponse
	if err := json.Unmarshal(put.Body.Bytes(), &binding); err != nil || binding.ID != callbackID || binding.EndpointID != endpoint.ID {
		t.Fatalf("binding = %#v, err=%v", binding, err)
	}
	if rec := e.do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("get binding: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodPut, path, request, nil); rec.Code != http.StatusOK {
		t.Fatalf("idempotent put: %d %s", rec.Code, rec.Body.String())
	}
	changed := request
	changed.ObjectID = "pi_other"
	if rec := e.do(t, http.MethodPut, path, changed, nil); rec.Code != http.StatusConflict {
		t.Fatalf("changed binding: %d %s", rec.Code, rec.Body.String())
	}
	invalid := request
	invalid.ObjectID = "pi_\x00"
	if rec := e.do(t, http.MethodPut, path, invalid, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid provider object ID: %d %s", rec.Code, rec.Body.String())
	}
	mustSeedApp(t, e, "foreign-binding-app")
	foreignEndpoint := mustCreateInboundWebhook(t, e, "foreign-binding-app")
	foreign := request
	foreign.EndpointID = foreignEndpoint.ID
	if rec := e.do(t, http.MethodPut, path, foreign, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign app endpoint bound to callback: %d %s", rec.Code, rec.Body.String())
	}

	body := []byte(`{"id":"evt_bound_1","object":"event","type":"payment_intent.succeeded","data":{"object":{"id":"pi_bound_1","object":"payment_intent"}}}`)
	bad := postStripeInboundWebhook(t, e, endpoint.EndpointURL, body, "wrong-secret")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad signature: %d %s", bad.Code, bad.Body.String())
	}
	if events, err := e.store.GetWorkflowEventsForRun(t.Context(), run.ID); err != nil || len(events) != 0 {
		t.Fatalf("bad signature completed callback: events=%#v err=%v", events, err)
	}
	unmatched := []byte(`{"id":"evt_unmatched","object":"event","type":"payment_intent.succeeded","data":{"object":{"id":"pi_other"}}}`)
	ordinary := postStripeInboundWebhook(t, e, endpoint.EndpointURL, unmatched, inboundWebhookTestSecret)
	if ordinary.Code != http.StatusAccepted {
		t.Fatalf("unmatched event: %d %s", ordinary.Code, ordinary.Body.String())
	}
	first := postStripeInboundWebhook(t, e, endpoint.EndpointURL, body, inboundWebhookTestSecret)
	if first.Code != http.StatusAccepted {
		t.Fatalf("matching event: %d %s", first.Code, first.Body.String())
	}
	var receipt api.WorkflowCallbackWebhookReceiptResponse
	if err := json.Unmarshal(first.Body.Bytes(), &receipt); err != nil || receipt.CallbackID != callbackID || receipt.Status != "received" || receipt.Duplicate {
		t.Fatalf("first receipt = %#v err=%v", receipt, err)
	}
	events, err := e.store.GetWorkflowEventsForRun(t.Context(), run.ID)
	if err != nil || len(events) != 1 || events[0].ID != callbackID {
		t.Fatalf("durable callback events = %#v err=%v", events, err)
	}
	second := postStripeInboundWebhook(t, e, endpoint.EndpointURL, body, inboundWebhookTestSecret)
	if err := json.Unmarshal(second.Body.Bytes(), &receipt); err != nil || second.Code != http.StatusAccepted || !receipt.Duplicate {
		t.Fatalf("duplicate receipt = %d %#v err=%v", second.Code, receipt, err)
	}
	changedBody := []byte(`{"id":"evt_bound_2","object":"event","type":"payment_intent.succeeded","data":{"object":{"id":"pi_bound_1","object":"payment_intent"}}, "extra":"changed"}`)
	ignored := postStripeInboundWebhook(t, e, endpoint.EndpointURL, changedBody, inboundWebhookTestSecret)
	if err := json.Unmarshal(ignored.Body.Bytes(), &receipt); err != nil || ignored.Code != http.StatusAccepted || receipt.Status != "ignored" {
		t.Fatalf("ignored receipt = %d %#v err=%v", ignored.Code, receipt, err)
	}
	if events, err := e.store.GetWorkflowEventsForRun(t.Context(), run.ID); err != nil || len(events) != 1 {
		t.Fatalf("extra event after conflicting callback: events=%#v err=%v", events, err)
	}
	if rec := e.do(t, http.MethodDelete, path, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete binding: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted binding: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, http.MethodPut, path, request, nil); rec.Code != http.StatusConflict {
		t.Fatalf("rebind completed callback: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := e.store.FindMatchingEvent(t.Context(), run.ID, api.WorkflowCallbackEventName(run.ID, "await")); err != nil {
		t.Fatal(err)
	}
	due, err := e.store.ListDueInvocations(t.Context(), events[0].ReceivedAt.AddDate(0, 0, 1), 10)
	if err != nil || len(due) != 1 || due[0].Source != state.InvocationInboundWebhook {
		t.Fatalf("unmatched event did not use ordinary ingress: due=%#v err=%v", due, err)
	}

	closedCreate := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/workflows/callback/runs", map[string]any{"request_id": "bound-closed"}, nil)
	var closedRun api.WorkflowRunResponse
	if closedCreate.Code != http.StatusCreated || json.Unmarshal(closedCreate.Body.Bytes(), &closedRun) != nil {
		t.Fatalf("create closed-run fixture: %d %s", closedCreate.Code, closedCreate.Body.String())
	}
	closedID := api.WorkflowCallbackID(closedRun.ID, "await")
	closedPath := fmt.Sprintf("/v1/workflows/runs/%s/callbacks/%s/webhook-binding", closedRun.ID, closedID)
	closedBinding := request
	closedBinding.ObjectID = "pi_closed"
	if rec := e.do(t, http.MethodPut, closedPath, closedBinding, nil); rec.Code != http.StatusOK {
		t.Fatalf("put closed-run binding: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := e.store.CancelWorkflowRun(t.Context(), closedRun.ID, "test"); err != nil {
		t.Fatal(err)
	}
	closedBody := []byte(`{"id":"evt_closed","object":"event","type":"payment_intent.succeeded","data":{"object":{"id":"pi_closed"}}}`)
	closedReceipt := postStripeInboundWebhook(t, e, endpoint.EndpointURL, closedBody, inboundWebhookTestSecret)
	if err := json.Unmarshal(closedReceipt.Body.Bytes(), &receipt); err != nil || closedReceipt.Code != http.StatusAccepted || receipt.Status != "ignored" {
		t.Fatalf("closed callback receipt = %d %#v err=%v", closedReceipt.Code, receipt, err)
	}
}

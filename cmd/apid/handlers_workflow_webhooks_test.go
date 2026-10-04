package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedWebhookAutomationAPI(t *testing.T) (testEnv, state.App, api.InboundWebhookEndpointResponse) {
	t.Helper()
	e := setupWebhookTest(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "webhook-automation")
	dep, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: "sha256:webhook", Workflows: json.RawMessage(`[{"name":"paid","steps":[{"name":"main","path":"/receipt","input":{"invoice":"{{input.data.data.object.id}}"}}]}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
		t.Fatal(err)
	}
	return e, app, mustCreateInboundWebhook(t, e, app.Slug)
}
func TestWebhookAutomationAPIReceiptsAndRouting(t *testing.T) {
	e, app, endpoint := seedWebhookAutomationAPI(t)
	path := "/v1/apps/" + app.Slug + "/inbound-webhooks/" + endpoint.ID
	zero := int64(0)
	body := api.PutWebhookAutomationBindingRequest{ExpectedVersion: &zero, WorkflowName: "paid", EventType: "invoice.*", TakeOverDelivery: true, Filter: json.RawMessage(`{"data":{"data":{"object":{"amount_paid":{"$gt":100}}}}}`)}
	headers := map[string]string{"Idempotency-Key": "bind-stripe-automation"}
	response := e.do(t, "PUT", path+"/automation-binding", body, headers)
	var binding api.WebhookAutomationBindingResponse
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &binding) != nil || binding.Version == 0 {
		t.Fatalf("bind=%d %s", response.Code, response.Body.String())
	}
	replay := e.do(t, "PUT", path+"/automation-binding", body, headers)
	if replay.Code != 200 || replay.Body.String() != response.Body.String() {
		t.Fatalf("replay=%d %s", replay.Code, replay.Body.String())
	}
	if stale := e.do(t, "PUT", path+"/automation-binding", body, nil); stale.Code != 409 {
		t.Fatalf("stale=%d %s", stale.Code, stale.Body.String())
	}
	providerBody := []byte(`{"id":"evt_start","type":"invoice.paid","data":{"object":{"id":"in_1","amount_paid":150}}}`)
	bad := postStripeInboundWebhook(t, e, endpoint.EndpointURL, providerBody, "wrong-secret")
	if bad.Code == 202 {
		t.Fatal("accepted invalid signature")
	}
	if _, err := e.store.GetWebhookAutomationReceipt(t.Context(), endpoint.ID, "evt_start"); err == nil {
		t.Fatal("bad signature wrote receipt")
	}
	first := postStripeInboundWebhook(t, e, endpoint.EndpointURL, providerBody, inboundWebhookTestSecret)
	var receipt api.WebhookAutomationReceiptResponse
	if first.Code != 202 || json.Unmarshal(first.Body.Bytes(), &receipt) != nil || receipt.Duplicate || receipt.Status != "accepted" {
		t.Fatalf("ingress=%d %s", first.Code, first.Body.String())
	}
	if _, err := e.store.InvocationByID(t.Context(), receipt.ReceiptID); err == nil {
		t.Fatal("automation ingress also queued app invocation")
	}
	duplicate := postStripeInboundWebhook(t, e, endpoint.EndpointURL, providerBody, inboundWebhookTestSecret)
	if duplicate.Code != 202 || json.Unmarshal(duplicate.Body.Bytes(), &receipt) != nil || !receipt.Duplicate {
		t.Fatalf("duplicate=%d %s", duplicate.Code, duplicate.Body.String())
	}
	work, err := e.store.ClaimDuePublishedEvent(t.Context(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(work.RecipientSnapshot) != 1 {
		t.Fatalf("recipients=%+v", work.RecipientSnapshot)
	}
	runID, err := e.store.AdmitEventWorkflow(t.Context(), work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	got := e.do(t, "GET", path+"/automation-receipts/evt_start", nil, nil)
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &receipt) != nil || receipt.RunID != runID {
		t.Fatalf("inspect=%d %s", got.Code, got.Body.String())
	}
	removed := e.do(t, "DELETE", path+"/automation-binding?expected_version="+strconv.FormatInt(binding.Version, 10), nil, nil)
	if removed.Code != 204 {
		t.Fatalf("remove=%d %s", removed.Code, removed.Body.String())
	}
	replay = postStripeInboundWebhook(t, e, endpoint.EndpointURL, providerBody, inboundWebhookTestSecret)
	if replay.Code != 202 || json.Unmarshal(replay.Body.Bytes(), &receipt) != nil || !receipt.Duplicate {
		t.Fatalf("retry after remove=%d %s", replay.Code, replay.Body.String())
	}
	if _, err := e.store.InvocationByID(t.Context(), receipt.ReceiptID); err == nil {
		t.Fatal("revocation changed an accepted event to app delivery")
	}
	future := postStripeInboundWebhook(t, e, endpoint.EndpointURL, []byte(`{"id":"evt_future","type":"invoice.paid"}`), inboundWebhookTestSecret)
	var invocation api.InboundWebhookReceiptResponse
	if future.Code != 202 || json.Unmarshal(future.Body.Bytes(), &invocation) != nil {
		t.Fatalf("future=%d %s", future.Code, future.Body.String())
	}
	if _, err := e.store.InvocationByID(t.Context(), invocation.ReceiptID); err != nil {
		t.Fatalf("ordinary delivery not restored: %v", err)
	}
}
func TestWebhookAutomationAPIValidationOwnershipAndRuntime(t *testing.T) {
	e, app, endpoint := seedWebhookAutomationAPI(t)
	path := "/v1/apps/" + app.Slug + "/inbound-webhooks/" + endpoint.ID
	for _, body := range []map[string]any{
		{"workflow_name": "paid", "event_type": "invoice.paid", "take_over_delivery": true},
		{"expected_version": 0, "workflow_name": "paid", "event_type": "invoice.paid", "take_over_delivery": false},
		{"expected_version": 0, "workflow_name": "paid", "event_type": "bad.*.type", "take_over_delivery": true},
		{"expected_version": 0, "workflow_name": "paid", "event_type": "invoice.paid", "take_over_delivery": true, "extra": true},
	} {
		response := e.do(t, "PUT", path+"/automation-binding", body, nil)
		if response.Code != 400 {
			t.Fatalf("invalid=%d %s", response.Code, response.Body.String())
		}
	}
	huge := e.do(t, "PUT", path+"/automation-binding", map[string]any{"padding": strings.Repeat("x", int(api.WorkflowWebhookBindingMaxBytes))}, nil)
	if huge.Code != 413 {
		t.Fatalf("size=%d %s", huge.Code, huge.Body.String())
	}
	zero := int64(0)
	response := e.do(t, "PUT", path+"/automation-binding", api.PutWebhookAutomationBindingRequest{ExpectedVersion: &zero, WorkflowName: "paid", EventType: "*", TakeOverDelivery: true}, nil)
	if response.Code != 200 {
		t.Fatalf("bind=%d %s", response.Code, response.Body.String())
	}
	foreign, err := e.store.CreateAccount(t.Context(), uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.store.CreateAPIKey(t.Context(), foreign.ID, hash, "foreign", api.ScopesAdminOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"/automation-binding", "/automation-receipts/evt_gate"} {
		response = e.do(t, http.MethodGet, path+suffix, nil, map[string]string{"Authorization": "Bearer " + token})
		if response.Code != 404 {
			t.Fatalf("foreign=%d %s", response.Code, response.Body.String())
		}
	}
	e.s.WithWorkflowRuntimeEnabled(false)
	providerBody := []byte(`{"id":"evt_gate","type":"invoice.paid"}`)
	response = postStripeInboundWebhook(t, e, endpoint.EndpointURL, providerBody, inboundWebhookTestSecret)
	if response.Code != 503 {
		t.Fatalf("runtime=%d %s", response.Code, response.Body.String())
	}
	if _, err := e.store.GetWebhookAutomationReceipt(t.Context(), endpoint.ID, "evt_gate"); err == nil {
		t.Fatal("unavailable runtime wrote acceptance")
	}
}

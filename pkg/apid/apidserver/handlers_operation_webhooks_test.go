package apidserver_test

// ADR-1193: a verified inbound webhook bound to a customer operation admits
// exactly one operation per provider event, owned by the bound customer.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperationWebhookBindingAdmitsOneOperationPerEvent(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "invoices")
	store, acct, ctx := *e.Store(), *e.Acct(), t.Context()
	(*e.S()).Operation().AdmissionEnabled = true
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	spec := api.OperationDefinitionSpec{Name: "invoice-paid", Method: "POST", Path: "/invoices", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"syncing"},
		InputSchema:  json.RawMessage(`{"type":"object","required":["webhook","payload"],"properties":{"webhook":{"type":"object","required":["event_id","event_type"]},"payload":{"type":"object","required":["id"]}}}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`)}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: appID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: spec}})
	if err != nil {
		t.Fatal(err)
	}
	alice, _, err := store.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: acct.ID, AppID: appID, Name: "alice", CertKind: state.CertKindPerHostSAN}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantSurface(ctx, acct.ID, alice.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	endpoint := mustCreateInboundWebhook(t, e, "invoices")
	bindingPath := "/v1/apps/invoices/inbound-webhooks/" + endpoint.ID + "/operation-binding"
	zero := int64(0)
	request := api.PutOperationWebhookBindingRequest{ExpectedVersion: &zero, Operation: "invoice-paid", PlatformTenantID: alice.ID, EventTypes: []string{"invoice.paid"}}
	expectStatus(t, e.Do(t, http.MethodPut, bindingPath, request, nil), http.StatusBadRequest) // must take over delivery explicitly
	request.TakeOverDelivery = true
	missing := request
	missing.Operation = "invoice-voided"
	expectStatus(t, e.Do(t, http.MethodPut, bindingPath, missing, nil), http.StatusNotFound)
	put := e.Do(t, http.MethodPut, bindingPath, request, nil)
	expectStatus(t, put, http.StatusOK)
	var binding api.OperationWebhookBindingResponse
	if err := json.Unmarshal(put.Body.Bytes(), &binding); err != nil || binding.Version <= 0 || binding.PlatformTenantID != alice.ID || binding.Scope != dep.Scope {
		t.Fatalf("binding response %s: %v", put.Body.String(), err)
	}
	expectStatus(t, e.Do(t, http.MethodPut, bindingPath, request, nil), http.StatusConflict) // stale expected version

	body := []byte(`{"id":"evt_1","type":"invoice.paid","data":{"object":{"customer":"cus_1"}}}`)
	first := postStripeInboundWebhook(t, e, endpoint.EndpointURL, body, inboundWebhookTestSecret)
	expectStatus(t, first, http.StatusAccepted)
	receipt := decodeInboundReceipt(t, first)
	if receipt.Status != "accepted" || receipt.Duplicate || first.Header().Get(api.OperationIDHeader) != receipt.ReceiptID {
		t.Fatalf("first delivery receipt %+v", receipt)
	}
	op, err := store.OperationByID(ctx, acct.ID, alice.ID, receipt.ReceiptID)
	if err != nil || op.DefinitionID != def.ID || op.DeploymentID != dep.ID {
		t.Fatalf("admitted operation %+v: %v", op, err)
	}
	inv, err := store.InvocationByID(ctx, op.CurrentInvocationID)
	if err != nil {
		t.Fatal(err)
	}
	var input api.OperationWebhookInput
	if err := json.Unmarshal(inv.Payload, &input); err != nil || input.Webhook.EventID != "evt_1" || input.Webhook.EventType != "invoice.paid" || input.Webhook.Provider != "stripe" || input.Webhook.Endpoint != endpoint.Name {
		t.Fatalf("operation input envelope %s: %v", inv.Payload, err)
	}
	plain := uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:inbound-webhook:"+endpoint.ID+"\x00evt_1")).String()
	if _, err := store.InvocationByID(ctx, plain); err == nil {
		t.Fatal("bound event also reached plain invocation delivery")
	}

	retry := postStripeInboundWebhook(t, e, endpoint.EndpointURL, body, inboundWebhookTestSecret)
	expectStatus(t, retry, http.StatusAccepted)
	if again := decodeInboundReceipt(t, retry); !again.Duplicate || again.ReceiptID != receipt.ReceiptID {
		t.Fatalf("provider retry receipt %+v", again)
	}
	changed := []byte(`{"id":"evt_1","type":"invoice.paid","data":{"object":{"customer":"cus_2"}}}`)
	expectStatus(t, postStripeInboundWebhook(t, e, endpoint.EndpointURL, changed, inboundWebhookTestSecret), http.StatusConflict)
	ignored := postStripeInboundWebhook(t, e, endpoint.EndpointURL, []byte(`{"id":"evt_2","type":"invoice.voided"}`), inboundWebhookTestSecret)
	expectStatus(t, ignored, http.StatusAccepted)
	if r := decodeInboundReceipt(t, ignored); r.Status != "ignored" {
		t.Fatalf("filtered event receipt %+v", r)
	}

	// Closed admission makes the provider retry new events; accepted events
	// are still acknowledged from their receipt.
	(*e.S()).Operation().AdmissionEnabled = false
	closed := postStripeInboundWebhook(t, e, endpoint.EndpointURL, []byte(`{"id":"evt_3","type":"invoice.paid"}`), inboundWebhookTestSecret)
	expectStatus(t, closed, http.StatusServiceUnavailable)
	if closed.Header().Get("Retry-After") == "" {
		t.Fatal("closed admission omitted Retry-After")
	}
	expectStatus(t, postStripeInboundWebhook(t, e, endpoint.EndpointURL, body, inboundWebhookTestSecret), http.StatusAccepted)

	got := e.Do(t, http.MethodGet, bindingPath, nil, nil)
	expectStatus(t, got, http.StatusOK)
	version := strconv.FormatInt(binding.Version, 10)
	expectStatus(t, e.Do(t, http.MethodDelete, bindingPath+"?expected_version="+url.QueryEscape(version+"9"), nil, nil), http.StatusConflict)
	expectStatus(t, e.Do(t, http.MethodDelete, bindingPath+"?expected_version="+version, nil, nil), http.StatusNoContent)
	expectStatus(t, e.Do(t, http.MethodGet, bindingPath, nil, nil), http.StatusNotFound)
	// A lost acknowledgement retried after unbinding must not reach plain delivery.
	late := postStripeInboundWebhook(t, e, endpoint.EndpointURL, body, inboundWebhookTestSecret)
	expectStatus(t, late, http.StatusAccepted)
	if r := decodeInboundReceipt(t, late); !r.Duplicate || r.ReceiptID != receipt.ReceiptID {
		t.Fatalf("retry after unbinding %+v", r)
	}
	if _, err := store.InvocationByID(ctx, plain); err == nil {
		t.Fatal("accepted event reached plain delivery after unbinding")
	}
	unbound := postStripeInboundWebhook(t, e, endpoint.EndpointURL, []byte(`{"id":"evt_4","type":"invoice.paid"}`), inboundWebhookTestSecret)
	expectStatus(t, unbound, http.StatusAccepted)
	if _, err := store.InvocationByID(ctx, decodeInboundReceipt(t, unbound).ReceiptID); err != nil {
		t.Fatalf("unbound endpoint did not return to plain delivery: %v", err)
	}
}

func decodeInboundReceipt(t *testing.T, rec *httptest.ResponseRecorder) api.InboundWebhookReceiptResponse {
	t.Helper()
	var receipt api.InboundWebhookReceiptResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("HTTP %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
}

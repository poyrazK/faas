// adr: 20261016100929
package apidserver_test

// A signal-mode operation binding turns verified provider events into
// signals for the bound customer's waiting Operations, matched by subject.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperationWebhookSignalBindingResumesWaitingOperations(t *testing.T) {
	e := setupWebhookTest(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "orders")
	store, acct, ctx := *e.Store(), *e.Acct(), t.Context()
	(*e.S()).Operation().AdmissionEnabled = true
	workflow := api.WorkflowSpec{Name: "order-chain", Steps: []api.WorkflowStepSpec{
		{Name: "validate", Path: "/validate"},
		{Name: "payment", WaitForEvent: "payment.succeeded", Timeout: time.Hour, DependsOn: []string{"validate"}},
		{Name: "finish", Path: "/finish", DependsOn: []string{"payment"}},
	}}
	workflows, _ := json.Marshal([]api.WorkflowSpec{workflow})
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage, Status: state.DeployLive, Workflows: workflows})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	spec := api.OperationDefinitionSpec{Name: "fulfil", Workflow: workflow.Name, Method: "POST", Path: "/orders", Owner: api.OperationOwnerPlatformTenant,
		InputSchema:    json.RawMessage(`{"type":"object","required":["order_id"],"properties":{"order_id":{"type":"string"}},"additionalProperties":false}`),
		OutputSchema:   json.RawMessage(`{"type":"object"}`),
		ProgressStages: []string{"validate", "payment", "finish"},
		Subject:        &api.OperationSubjectSpec{Type: "order", IDFrom: "/order_id"}}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: appID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: spec}})
	if err != nil {
		t.Fatal(err)
	}
	tenants := map[string]state.PlatformTenant{}
	for _, name := range []string{"alice", "bob"} {
		tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: acct.ID, AppID: appID, Name: name, CertKind: state.CertKindPerHostSAN}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.LinkPlatformTenantSurface(ctx, acct.ID, tenant.ID, surface.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
			t.Fatal(err)
		}
		tenants[name] = tenant
	}
	admit := func(tenant, key string) state.Operation {
		t.Helper()
		op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenants[tenant].ID, IdempotencyKey: key, Input: json.RawMessage(`{"order_id":"ord_1"}`)})
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	alice := admit("alice", "order-alice")
	run, err := store.ClaimNextDueWorkflowRun(ctx)
	if err != nil || run == nil || run.ID != alice.WorkflowRunID {
		t.Fatalf("claim alice's run: %+v %v", run, err)
	}
	if err := store.MarkWorkflowStepStatus(ctx, run.ID, "validate", state.WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"ok":true}`), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ParkWorkflowEvent(ctx, run.ID, "payment", "payment.succeeded", time.Hour); err != nil {
		t.Fatal(err)
	}
	bob := admit("bob", "order-bob") // same subject, another customer

	endpoint := mustCreateInboundWebhook(t, e, "orders")
	bindingPath := "/v1/apps/orders/inbound-webhooks/" + endpoint.ID + "/operation-binding"
	zero := int64(0)
	request := api.PutOperationWebhookBindingRequest{ExpectedVersion: &zero, Operation: "fulfil", PlatformTenantID: tenants["alice"].ID, TakeOverDelivery: true,
		Mode: api.OperationWebhookModeSignal, Subject: &api.OperationSubjectSpec{Type: "order", IDFrom: "/data/object/metadata/order_id"},
		Signals: []api.OperationWebhookSignal{{ProviderEvent: "payment_intent.succeeded", Event: "payment.succeeded"}}}
	for name, mutate := range map[string]func(*api.PutOperationWebhookBindingRequest){
		"subject type": func(r *api.PutOperationWebhookBindingRequest) {
			r.Subject = &api.OperationSubjectSpec{Type: "invoice", IDFrom: "/id"}
		},
		"unexpected event": func(r *api.PutOperationWebhookBindingRequest) {
			r.Signals = []api.OperationWebhookSignal{{ProviderEvent: "charge.refunded", Event: "refund.done"}}
		},
		"event type filter": func(r *api.PutOperationWebhookBindingRequest) { r.EventTypes = []string{"payment_intent.succeeded"} },
		"no signals":        func(r *api.PutOperationWebhookBindingRequest) { r.Signals = nil },
	} {
		bad := request
		mutate(&bad)
		if rec := e.Do(t, http.MethodPut, bindingPath, bad, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: HTTP %d %s", name, rec.Code, rec.Body.String())
		}
	}
	put := e.Do(t, http.MethodPut, bindingPath, request, nil)
	expectStatus(t, put, http.StatusOK)
	var binding api.OperationWebhookBindingResponse
	if err := json.Unmarshal(put.Body.Bytes(), &binding); err != nil || binding.Mode != api.OperationWebhookModeSignal || len(binding.Signals) != 1 || binding.Subject == nil {
		t.Fatalf("binding response %s: %v", put.Body.String(), err)
	}

	body := []byte(`{"id":"evt_1","type":"payment_intent.succeeded","data":{"object":{"amount":100,"metadata":{"order_id":"ord_1"}}}}`)
	first := postStripeInboundWebhook(t, e, endpoint.EndpointURL, body, inboundWebhookTestSecret)
	expectStatus(t, first, http.StatusAccepted)
	if r := decodeInboundReceipt(t, first); r.Status != api.InboundWebhookSignaled || r.Duplicate {
		t.Fatalf("first delivery %+v", r)
	}
	signalled := func(op state.Operation) int {
		t.Helper()
		page, err := store.OperationEvents(ctx, acct.ID, "", op.ID, 0, api.OperationEventsPageMax)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, event := range page.Events {
			if event.Type == "signal_received" {
				n++
			}
		}
		return n
	}
	if signalled(alice) != 1 || signalled(bob) != 0 {
		t.Fatalf("signals reached alice=%d bob=%d", signalled(alice), signalled(bob))
	}
	retry := postStripeInboundWebhook(t, e, endpoint.EndpointURL, body, inboundWebhookTestSecret)
	expectStatus(t, retry, http.StatusAccepted)
	if r := decodeInboundReceipt(t, retry); r.Status != api.InboundWebhookSignaled || !r.Duplicate || signalled(alice) != 1 {
		t.Fatalf("provider retry %+v, signals %d", r, signalled(alice))
	}
	for name, event := range map[string][]byte{
		"unmapped type":   []byte(`{"id":"evt_2","type":"payment_intent.created","data":{"object":{"metadata":{"order_id":"ord_1"}}}}`),
		"missing subject": []byte(`{"id":"evt_3","type":"payment_intent.succeeded","data":{"object":{"metadata":{}}}}`),
	} {
		rec := postStripeInboundWebhook(t, e, endpoint.EndpointURL, event, inboundWebhookTestSecret)
		expectStatus(t, rec, http.StatusAccepted)
		if r := decodeInboundReceipt(t, rec); r.Status != "ignored" {
			t.Fatalf("%s: receipt %+v", name, r)
		}
	}
	none := postStripeInboundWebhook(t, e, endpoint.EndpointURL, []byte(`{"id":"evt_4","type":"payment_intent.succeeded","data":{"object":{"metadata":{"order_id":"ord_9"}}}}`), inboundWebhookTestSecret)
	expectStatus(t, none, http.StatusAccepted)
	if r := decodeInboundReceipt(t, none); r.Status != api.InboundWebhookUnmatched {
		t.Fatalf("unmatched subject receipt %+v", r)
	}
	plain := uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:inbound-webhook:"+endpoint.ID+"\x00evt_1")).String()
	if _, err := store.InvocationByID(ctx, plain); err == nil {
		t.Fatal("a signal-bound event also reached plain invocation delivery")
	}
}

// adr: 20261017100853
package apidserver_test

// A customer signals its own waiting Operations with its tenant credentials,
// by ID or by business subject, and never reaches another customer's.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlatformTenantSelfOperationSignals(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "operation-tenant-signals@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "operation-tenant-signals", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	(*srv.Operation()).AdmissionEnabled, (*srv.WorkflowRuntimeEnabled()) = true, true
	handler := srv.Handler()
	do := func(method, path, bearer string, body any, idempotency string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewReader(mustJSON(t, body)))
		req.Header.Set("Authorization", "Bearer "+bearer)
		if idempotency != "" {
			req.Header.Set("Idempotency-Key", idempotency)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	check := func(rec *httptest.ResponseRecorder, status int) {
		t.Helper()
		if rec.Code != status {
			t.Fatalf("HTTP %d, want %d: %s", rec.Code, status, rec.Body.String())
		}
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "orders", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	workflow := api.WorkflowSpec{Name: "order-chain", Steps: []api.WorkflowStepSpec{
		{Name: "validate", Path: "/validate"},
		{Name: "payment", WaitForEvent: "payment.succeeded", Timeout: time.Hour, DependsOn: []string{"validate"}},
		{Name: "finish", Path: "/finish", DependsOn: []string{"payment"}},
	}}
	workflows, _ := json.Marshal([]api.WorkflowSpec{workflow})
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Workflows: workflows})
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
	defined := do("PUT", "/v1/apps/orders/deployments/"+dep.ID+"/operation-definitions/fulfil", key, spec, "")
	check(defined, http.StatusOK)
	var def api.OperationDefinitionResponse
	if err := json.Unmarshal(defined.Body.Bytes(), &def); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	mint := func(tenant state.PlatformTenant, name string, scopes ...string) {
		t.Helper()
		minted := do("POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", key, api.CreatePlatformTenantAccessTokenRequest{Name: name, Scopes: scopes}, "")
		check(minted, http.StatusCreated)
		var token api.CreatePlatformTenantAccessTokenResponse
		if err := json.Unmarshal(minted.Body.Bytes(), &token); err != nil {
			t.Fatal(err)
		}
		tokens[name] = token.Token
	}
	for _, name := range []string{"alice", "bob"} {
		tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: acct.ID, AppID: app.ID, Name: name, CertKind: state.CertKindPerHostSAN}, api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.LinkPlatformTenantSurface(ctx, acct.ID, tenant.ID, surface.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
			t.Fatal(err)
		}
		mint(tenant, name, api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage)
		if name == "alice" {
			mint(tenant, "alice-read", api.ScopePlatformTenantOperationsRead)
		}
	}
	start := func(tenant string) api.OperationAcceptedResponse {
		t.Helper()
		started := do("POST", "/v1/platform-tenant-self/customer-operations", tokens[tenant], api.OperationStartRequest{DefinitionID: def.ID, Input: json.RawMessage(`{"order_id":"ord_1"}`)}, "order-"+tenant)
		check(started, http.StatusAccepted)
		var receipt api.OperationAcceptedResponse
		if err := json.Unmarshal(started.Body.Bytes(), &receipt); err != nil {
			t.Fatal(err)
		}
		return receipt
	}
	alice, bob := start("alice"), start("bob") // the same order subject
	// Park alice's run on its event wait, as the scheduler would.
	run, err := store.ClaimNextDueWorkflowRun(ctx)
	if err != nil || run == nil {
		t.Fatalf("claim: %+v %v", run, err)
	}
	if op, ok, err := store.OperationForWorkflowRun(ctx, run.ID); err != nil || !ok || op.ID != alice.ID {
		t.Fatalf("claimed %s, want alice's run: %v", op.ID, err)
	}
	if err := store.MarkWorkflowStepStatus(ctx, run.ID, "validate", state.WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"ok":true}`), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ParkWorkflowEvent(ctx, run.ID, "payment", "payment.succeeded", time.Hour); err != nil {
		t.Fatal(err)
	}

	signal := api.OperationSignalRequest{SignalID: "pay-1", Event: "payment.succeeded", Payload: json.RawMessage(`{"charge":"ch_1"}`)}
	alicePath := "/v1/platform-tenant-self/customer-operations/" + alice.ID + "/signals"
	check(do("POST", alicePath, tokens["bob"], signal, ""), http.StatusNotFound)
	check(do("POST", alicePath, tokens["alice-read"], signal, ""), http.StatusForbidden)
	first := do("POST", alicePath, tokens["alice"], signal, "")
	check(first, http.StatusOK)
	var receipt api.OperationSignalReceipt
	if err := json.Unmarshal(first.Body.Bytes(), &receipt); err != nil || receipt.OperationID != alice.ID || !receipt.Waiting || receipt.Duplicate {
		t.Fatalf("first signal %s: %v", first.Body.String(), err)
	}
	repeat := do("POST", alicePath, tokens["alice"], signal, "")
	check(repeat, http.StatusOK)
	if err := json.Unmarshal(repeat.Body.Bytes(), &receipt); err != nil || !receipt.Duplicate {
		t.Fatalf("repeat %s: %v", repeat.Body.String(), err)
	}
	check(do("POST", alicePath, tokens["alice"], api.OperationSignalRequest{SignalID: "pay-2", Event: "refund.done"}, ""), http.StatusUnprocessableEntity)

	// By subject: bob reaches only his own Operation about ord_1.
	bySubject := api.PlatformTenantSubjectSignalRequest{DefinitionID: def.ID, Subject: api.OperationSubject{Type: "order", ID: "ord_1"}, SignalID: "pay-bob", Event: "payment.succeeded"}
	reached := do("POST", "/v1/platform-tenant-self/customer-operation-signals", tokens["bob"], bySubject, "")
	check(reached, http.StatusOK)
	var page api.OperationSubjectSignalResponse
	if err := json.Unmarshal(reached.Body.Bytes(), &page); err != nil || len(page.Delivered) != 1 || page.Delivered[0].OperationID != bob.ID || page.Delivered[0].Waiting {
		t.Fatalf("bob's subject signal %s: %v", reached.Body.String(), err)
	}
	bySubject.Subject.ID = "ord_9"
	none := do("POST", "/v1/platform-tenant-self/customer-operation-signals", tokens["bob"], bySubject, "")
	check(none, http.StatusOK)
	if err := json.Unmarshal(none.Body.Bytes(), &page); err != nil || len(page.Delivered) != 0 {
		t.Fatalf("unknown subject %s: %v", none.Body.String(), err)
	}
	bySubject.DefinitionID = "33333333-3333-3333-3333-333333333333"
	check(do("POST", "/v1/platform-tenant-self/customer-operation-signals", tokens["bob"], bySubject, ""), http.StatusNotFound)
	check(do("POST", "/v1/platform-tenant-self/customer-operation-signals", tokens["alice-read"], bySubject, ""), http.StatusForbidden)

	// The history records who delivered each signal.
	sources := func(id, bearer string) []string {
		t.Helper()
		events := do("GET", "/v1/platform-tenant-self/customer-operations/"+id+"/events", bearer, nil, "")
		check(events, http.StatusOK)
		var history api.OperationEventsResponse
		if err := json.Unmarshal(events.Body.Bytes(), &history); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, event := range history.Events {
			if event.Type != "signal_received" {
				continue
			}
			var data struct {
				Source string `json:"source"`
			}
			_ = json.Unmarshal(event.Data, &data)
			out = append(out, data.Source)
		}
		return out
	}
	check(do("POST", "/v1/apps/orders/operations/"+bob.ID+"/signals", key, api.OperationSignalRequest{SignalID: "pay-account", Event: "payment.succeeded"}, ""), http.StatusOK)
	if got := sources(alice.ID, tokens["alice"]); len(got) != 1 || got[0] != api.OperationSignalSourceCustomer {
		t.Fatalf("alice's signal sources %v", got)
	}
	if got := sources(bob.ID, tokens["bob"]); len(got) != 2 || got[0] != api.OperationSignalSourceCustomer || got[1] != api.OperationSignalSourceAccount {
		t.Fatalf("bob's signal sources %v", got)
	}
}

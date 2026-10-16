// adr: 1197
package apidserver_test

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

func TestOperationSignalRoutes(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "operation-signals@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "operation-signals", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	reader, readHash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, readHash, "operation-signals-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	(*srv.Operation()).AdmissionEnabled, (*srv.WorkflowRuntimeEnabled()) = true, true
	handler := srv.Handler()
	do := func(method, path, bearer string, body any) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewReader(mustJSON(t, body)))
		req.Header.Set("Authorization", "Bearer "+bearer)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	check := func(rec *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		if rec.Code != status {
			t.Fatalf("HTTP %d, want %d: %s", rec.Code, status, rec.Body.String())
		}
		if code != "" {
			var problem struct {
				Code string `json:"code"`
			}
			if json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != code {
				t.Fatalf("problem %s, want code %s", rec.Body.String(), code)
			}
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
	defined := do("PUT", "/v1/apps/orders/deployments/"+dep.ID+"/operation-definitions/fulfil", key, spec)
	check(defined, http.StatusOK, "")
	var def api.OperationDefinitionResponse
	if err := json.Unmarshal(defined.Body.Bytes(), &def); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: acct.ID, AppID: app.ID, Name: "alice", CertKind: state.CertKindPerHostSAN}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantSurface(ctx, acct.ID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "order-1", Input: json.RawMessage(`{"order_id":"ord_1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.ClaimNextDueWorkflowRun(ctx)
	if err != nil || run == nil {
		t.Fatalf("claim operation run: %+v %v", run, err)
	}
	if err := store.MarkWorkflowStepStatus(ctx, run.ID, "validate", state.WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"ok":true}`), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ParkWorkflowEvent(ctx, run.ID, "payment", "payment.succeeded", time.Hour); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/orders/operations/" + op.ID + "/signals"
	signal := api.OperationSignalRequest{SignalID: "evt_1", Event: "payment.succeeded", Payload: json.RawMessage(`{"charge":"ch_1"}`)}
	check(do("POST", path, reader, signal), http.StatusForbidden, "")
	check(do("POST", "/v1/workflows/runs/"+run.ID+"/events", key, api.InjectWorkflowEventRequest{EventName: "payment.succeeded"}), http.StatusConflict, "operation_signal_required")
	check(do("POST", path, key, api.OperationSignalRequest{SignalID: "evt_x", Event: "payment.failed"}), http.StatusUnprocessableEntity, "operation_signal_unexpected")
	check(do("POST", path, key, api.OperationSignalRequest{SignalID: "", Event: "payment.succeeded"}), http.StatusBadRequest, "")
	delivered := do("POST", path, key, signal)
	check(delivered, http.StatusOK, "")
	var receipt api.OperationSignalReceipt
	if err := json.Unmarshal(delivered.Body.Bytes(), &receipt); err != nil || receipt.OperationID != op.ID || !receipt.Waiting || receipt.Duplicate {
		t.Fatalf("receipt %s: %v", delivered.Body.String(), err)
	}
	repeat := do("POST", path, key, signal)
	check(repeat, http.StatusOK, "")
	if err := json.Unmarshal(repeat.Body.Bytes(), &receipt); err != nil || !receipt.Duplicate {
		t.Fatalf("repeat receipt %s: %v", repeat.Body.String(), err)
	}
	signal.Payload = json.RawMessage(`{"charge":"ch_2"}`)
	check(do("POST", path, key, signal), http.StatusConflict, "operation_signal_conflict")

	subjectPath := "/v1/apps/orders/operation-signals"
	bySubject := api.OperationSubjectSignalRequest{Scope: op.Scope, Operation: "fulfil", Subject: api.OperationSubject{Type: "order", ID: "ord_1"}, SignalID: "evt_1", Event: "payment.succeeded", Payload: json.RawMessage(`{"charge":"ch_1"}`)}
	fanout := do("POST", subjectPath, key, bySubject)
	check(fanout, http.StatusOK, "")
	var response api.OperationSubjectSignalResponse
	if err := json.Unmarshal(fanout.Body.Bytes(), &response); err != nil || len(response.Delivered) != 1 || !response.Delivered[0].Duplicate {
		t.Fatalf("subject signal %s: %v", fanout.Body.String(), err)
	}
	bySubject.Subject.ID = "ord_missing"
	none := do("POST", subjectPath, key, bySubject)
	check(none, http.StatusOK, "")
	if err := json.Unmarshal(none.Body.Bytes(), &response); err != nil || len(response.Delivered) != 0 {
		t.Fatalf("unmatched subject %s: %v", none.Body.String(), err)
	}
	bySubject.Scope = "Not A Scope!"
	check(do("POST", subjectPath, key, bySubject), http.StatusBadRequest, "")
	check(do("POST", subjectPath, reader, bySubject), http.StatusForbidden, "")
}

package apidserver_test

// ADR-1194: only the declared approver decides a waiting operation, through
// the approval routes; generic callbacks cannot resume an operation run.

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

func TestOperationApprovalRoutes(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "operation-approvals@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "operation-approvals", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	(*srv.Operation()).AdmissionEnabled, (*srv.WorkflowRuntimeEnabled()) = true, true
	handler := srv.Handler()
	do := func(method, path, bearer string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+bearer)
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
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "refunds", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	workflow := api.WorkflowSpec{Name: "refund-chain", Steps: []api.WorkflowStepSpec{
		{Name: "validate", Path: "/validate"},
		{Name: "review", WaitForCallback: true, Timeout: time.Hour, DependsOn: []string{"validate"}},
		{Name: "refund", Path: "/refund", DependsOn: []string{"review"}},
	}}
	workflows, _ := json.Marshal([]api.WorkflowSpec{workflow})
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Workflows: workflows})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	spec := api.OperationDefinitionSpec{Name: "refund", Workflow: workflow.Name, Method: "POST", Path: "/refunds", Owner: api.OperationOwnerPlatformTenant,
		InputSchema:    json.RawMessage(`{"type":"object","required":["count"],"properties":{"count":{"type":"integer"}},"additionalProperties":false}`),
		OutputSchema:   json.RawMessage(`{"type":"object"}`),
		ProgressStages: []string{"validate", "review", "refund"},
		Approvals:      []api.OperationApprovalSpec{{Step: "review", Approver: api.OperationApproverCustomer}}}
	defined := do("PUT", "/v1/apps/refunds/deployments/"+dep.ID+"/operation-definitions/refund", key, spec)
	check(defined, http.StatusOK)
	var def api.OperationDefinitionResponse
	if err := json.Unmarshal(defined.Body.Bytes(), &def); err != nil || len(def.Spec.Approvals) != 1 {
		t.Fatalf("approval definition %s: %v", defined.Body.String(), err)
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
	minted := do("POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", key, api.CreatePlatformTenantAccessTokenRequest{Name: "browser", Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}})
	check(minted, http.StatusCreated)
	var token api.CreatePlatformTenantAccessTokenResponse
	if err := json.Unmarshal(minted.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/platform-tenant-self/customer-operations", bytes.NewReader(mustJSON(t, api.OperationStartRequest{DefinitionID: def.ID, Input: json.RawMessage(`{"count":1}`)})))
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Idempotency-Key", "refund-1")
	started := httptest.NewRecorder()
	handler.ServeHTTP(started, req)
	check(started, http.StatusAccepted)
	var receipt api.OperationAcceptedResponse
	if err := json.Unmarshal(started.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	// Advance the run to its approval exactly as the scheduler would.
	run, err := store.ClaimNextDueWorkflowRun(ctx)
	if err != nil || run == nil {
		t.Fatalf("claim operation run: %+v %v", run, err)
	}
	if err := store.MarkWorkflowStepStatus(ctx, run.ID, "validate", state.WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"ok":true}`), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ParkWorkflowEvent(ctx, run.ID, "review", api.WorkflowCallbackEventName(run.ID, "review"), time.Hour); err != nil {
		t.Fatal(err)
	}
	read := do("GET", receipt.StatusURL, token.Token, nil)
	check(read, http.StatusOK)
	var waiting api.OperationResponse
	if err := json.Unmarshal(read.Body.Bytes(), &waiting); err != nil || waiting.State != api.OperationWaiting || waiting.Waiting == nil || waiting.Waiting.Kind != api.OperationWaitApproval || waiting.Waiting.Approver != api.OperationApproverCustomer {
		t.Fatalf("waiting operation %s: %v", read.Body.String(), err)
	}
	decision := api.OperationApprovalRequest{ExpectedGeneration: 1, Decision: api.OperationApprovalApproved, Comment: "looks right"}
	approvalPath := "/v1/platform-tenant-self/customer-operations/" + receipt.ID + "/approvals/review"
	check(do("POST", "/v1/apps/refunds/operations/"+receipt.ID+"/approvals/review", key, decision), http.StatusForbidden)
	check(do("POST", "/v1/platform-tenant-self/customer-operations/"+receipt.ID+"/approvals/validate", token.Token, decision), http.StatusNotFound)
	check(do("POST", "/v1/workflows/runs/"+run.ID+"/callbacks/"+api.WorkflowCallbackID(run.ID, "review"), key, map[string]string{"approval": "forged"}), http.StatusConflict)
	approved := do("POST", approvalPath, token.Token, decision)
	check(approved, http.StatusOK)
	check(do("POST", approvalPath, token.Token, decision), http.StatusOK)
	decision.Decision = api.OperationApprovalRejected
	check(do("POST", approvalPath, token.Token, decision), http.StatusConflict)
	events := do("GET", receipt.EventsURL, token.Token, nil)
	check(events, http.StatusOK)
	var page api.OperationEventsResponse
	if err := json.Unmarshal(events.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	var recorded *api.OperationEvent
	for i := range page.Events {
		if page.Events[i].Type == "approval_decided" {
			recorded = &page.Events[i]
		}
	}
	if recorded == nil {
		t.Fatalf("history lost the decision: %s", events.Body.String())
	}
	var data struct {
		Step     string                     `json:"step"`
		Decision string                     `json:"decision"`
		Actor    api.OperationApprovalActor `json:"actor"`
	}
	if err := json.Unmarshal(recorded.Data, &data); err != nil || data.Step != "review" || data.Decision != api.OperationApprovalApproved || data.Actor.Kind != api.OperationApproverCustomer || data.Actor.ID == "" {
		t.Fatalf("decision event %s: %v", recorded.Data, err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTenantWorkflowDefinitionSupport(t *testing.T) {
	outbound := &api.WorkflowOutboundSpec{IntegrationID: "integration", Method: http.MethodPost, Path: "/v1/items"}
	for _, spec := range []api.WorkflowSpec{
		{Name: "outbound", Steps: []api.WorkflowStepSpec{{Name: "send", Outbound: outbound}}},
		{Name: "foreach-outbound", Steps: []api.WorkflowStepSpec{{Name: "send", ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Outbound: outbound}}}}},
	} {
		if !tenantWorkflowDefinitionSupported(spec) {
			t.Errorf("tenant-bound workflow with app-bound outbound action %q was rejected", spec.Name)
		}
	}
	for _, spec := range []api.WorkflowSpec{
		{Name: "event", Steps: []api.WorkflowStepSpec{{Name: "wait", WaitForEvent: "order.approved"}}},
		{Name: "callback", Steps: []api.WorkflowStepSpec{{Name: "wait", WaitForCallback: true}}},
	} {
		if tenantWorkflowDefinitionSupported(spec) {
			t.Errorf("tenant-bound workflow with external continuation %q was accepted", spec.Name)
		}
	}
}

func seedWorkflowApp(t *testing.T, e testEnv, slug string) state.App {
	t.Helper()
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID,
		Slug:      slug,
		Type:      state.AppTypeFunction,
		Runtime:   "node22",
		RAMMB:     256,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateApp(%q): %v", slug, err)
	}
	definitions := []api.WorkflowSpec{
		{Name: "process-order", Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/process-order"}}},
		{Name: "w1", Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/w1"}}},
		{Name: "fulfill-transaction", Steps: []api.WorkflowStepSpec{{Name: "commit", Path: "/fulfill", ManagedOperation: true}}},
		{Name: "approval", Steps: []api.WorkflowStepSpec{{Name: "step_one", WaitForEvent: "manager.approved", Timeout: time.Hour}}},
		{Name: "callback", Steps: []api.WorkflowStepSpec{{Name: "await", WaitForCallback: true, Timeout: time.Hour}}},
	}
	raw, err := json.Marshal(definitions)
	if err != nil {
		t.Fatalf("marshal workflow definitions: %v", err)
	}
	if _, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "registry.example.com/test@sha256:" + strings.Repeat("a", 64),
		Status: state.DeployLive, Workflows: raw,
	}); err != nil {
		t.Fatalf("CreateDeployment(%q): %v", slug, err)
	}
	return app
}

func TestWorkflowCallbacks_AuthenticatedOneTimeCompletion(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "callback-app")
	create := e.do(t, "POST", fmt.Sprintf("/v1/apps/%s/workflows/callback/runs", app.Slug), map[string]any{"request_id": "r1"}, nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("create callback workflow = %d: %s", create.Code, create.Body.String())
	}
	var run api.WorkflowRunResponse
	if err := json.Unmarshal(create.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/v1/workflows/runs/%s/callbacks", run.ID)
	list := e.do(t, "GET", base, nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list callbacks = %d: %s", list.Code, list.Body.String())
	}
	var callbacks api.ListWorkflowCallbacksResponse
	if err := json.Unmarshal(list.Body.Bytes(), &callbacks); err != nil {
		t.Fatal(err)
	}
	if len(callbacks.Callbacks) != 1 || callbacks.Callbacks[0].StepName != "await" || callbacks.Callbacks[0].ExpiresAt != nil {
		t.Fatalf("callbacks = %#v", callbacks.Callbacks)
	}
	callbackID := callbacks.Callbacks[0].ID
	completeURL := base + "/" + callbackID
	first := e.do(t, "POST", completeURL, map[string]any{"approved": true}, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first callback = %d: %s", first.Code, first.Body.String())
	}
	var firstReceipt api.CompleteWorkflowCallbackResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstReceipt); err != nil || firstReceipt.Duplicate {
		t.Fatalf("first receipt = %#v, err=%v", firstReceipt, err)
	}
	retry := e.do(t, "POST", completeURL, map[string]any{"approved": true}, nil)
	var retryReceipt api.CompleteWorkflowCallbackResponse
	if err := json.Unmarshal(retry.Body.Bytes(), &retryReceipt); err != nil || retry.Code != http.StatusOK || !retryReceipt.Duplicate {
		t.Fatalf("retry = %d %#v, err=%v", retry.Code, retryReceipt, err)
	}
	conflict := e.do(t, "POST", completeURL, map[string]any{"approved": false}, nil)
	assertProblem(t, conflict, http.StatusConflict, api.CodeWorkflowCallbackPayloadConflict)
	unknown := e.do(t, "POST", base+"/00000000-0000-0000-0000-000000000000", map[string]any{"approved": true}, nil)
	assertProblem(t, unknown, http.StatusNotFound, api.CodeWorkflowStepNotFound)
	reserved := e.do(t, "POST", fmt.Sprintf("/v1/workflows/runs/%s/events", run.ID), api.InjectWorkflowEventRequest{
		EventName: api.WorkflowCallbackEventName(run.ID, "await"), Payload: json.RawMessage("{\"approved\":true}"),
	}, nil)
	assertProblem(t, reserved, http.StatusBadRequest, api.CodeValidation)

	secondCreate := e.do(t, "POST", fmt.Sprintf("/v1/apps/%s/workflows/callback/runs", app.Slug), map[string]any{"request_id": "r2"}, nil)
	var second api.WorkflowRunResponse
	if err := json.Unmarshal(secondCreate.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if err := e.store.CreateWorkflowSteps(context.Background(), second.ID, []*state.WorkflowStep{{StepName: "await"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.store.ParkWorkflowEvent(context.Background(), second.ID, "await", api.WorkflowCallbackEventName(second.ID, "await"), time.Hour); err != nil {
		t.Fatal(err)
	}
	parkedList := e.do(t, "GET", fmt.Sprintf("/v1/workflows/runs/%s/callbacks", second.ID), nil, nil)
	var parked api.ListWorkflowCallbacksResponse
	if err := json.Unmarshal(parkedList.Body.Bytes(), &parked); err != nil || len(parked.Callbacks) != 1 || parked.Callbacks[0].ExpiresAt == nil {
		t.Fatalf("parked callbacks = %#v, err=%v", parked.Callbacks, err)
	}
	if _, err := e.store.CancelWorkflowRun(context.Background(), second.ID, "test"); err != nil {
		t.Fatal(err)
	}
	closed := e.do(t, "POST", fmt.Sprintf("/v1/workflows/runs/%s/callbacks/%s", second.ID, parked.Callbacks[0].ID), map[string]any{"approved": true}, nil)
	assertProblem(t, closed, http.StatusConflict, api.CodeWorkflowCallbackClosed)
}

func TestCreateWorkflowRun_FreePlan_PaymentRequired(t *testing.T) {
	e := setup(t, api.PlanFree)
	app := seedWorkflowApp(t, e, "free-app")

	rec := e.do(t, "POST", fmt.Sprintf("/v1/apps/%s/workflows/signup/runs", app.Slug), map[string]any{
		"user_id": "u123",
	}, nil)

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402 Payment Required, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), api.CodePlanWorkflowsNotAllowed) {
		t.Fatalf("expected error code %s in body: %s", api.CodePlanWorkflowsNotAllowed, rec.Body.String())
	}
}

func TestCreateWorkflowRun_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "hobby-app")

	rec := e.do(t, "POST", fmt.Sprintf("/v1/apps/%s/workflows/process-order/runs", app.Slug), map[string]any{
		"order_id": "ord_999",
	}, nil)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var resp api.WorkflowRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.WorkflowName != "process-order" {
		t.Fatalf("expected workflow process-order, got %s", resp.WorkflowName)
	}
	if resp.Status != state.WorkflowRunStatusPending {
		t.Fatalf("expected pending status, got %s", resp.Status)
	}
	stored, err := e.store.GetWorkflowRun(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if !strings.Contains(string(stored.DefinitionSnapshot), "/process-order") {
		t.Fatalf("definition snapshot = %s, want live deployment definition", stored.DefinitionSnapshot)
	}
}

func TestCreateWorkflowRun_IdempotencyKeyReplaysAndRejectsChangedInput(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "idempotent-run-app")
	path := fmt.Sprintf("/v1/apps/%s/workflows/process-order/runs", app.Slug)
	headers := map[string]string{"Idempotency-Key": "invoice-paid-event-42"}
	first := e.do(t, http.MethodPost, path, json.RawMessage(`{"order_id":"ord_42","currency":"usd"}`), headers)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create = %d: %s", first.Code, first.Body.String())
	}
	var firstRun api.WorkflowRunResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstRun); err != nil {
		t.Fatal(err)
	}

	// JSON object ordering and insignificant whitespace do not change the
	// request fingerprint, and replay survives a runtime outage.
	e.s.WithWorkflowRuntimeEnabled(false)
	replay := e.do(t, http.MethodPost, path, json.RawMessage("{ \"currency\": \"usd\", \"order_id\": \"ord_42\" }"), headers)
	if replay.Code != http.StatusCreated || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay = %d headers=%v: %s", replay.Code, replay.Header(), replay.Body.String())
	}
	var replayedRun api.WorkflowRunResponse
	if err := json.Unmarshal(replay.Body.Bytes(), &replayedRun); err != nil || replayedRun.ID != firstRun.ID {
		t.Fatalf("replayed run = %+v, err=%v; want original %s", replayedRun, err, firstRun.ID)
	}

	conflict := e.do(t, http.MethodPost, path, json.RawMessage(`{"order_id":"ord_43","currency":"usd"}`), headers)
	assertProblem(t, conflict, http.StatusConflict, api.CodeConflict)
	active, err := e.store.CountActiveRunsByApp(context.Background(), app.ID)
	if err != nil || active != 1 {
		t.Fatalf("active runs after replay/conflict = %d, err=%v; want 1", active, err)
	}
}

func TestCreateWorkflowRun_RejectsInvalidIdempotencyKey(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "invalid-idempotency-key-app")
	rec := e.do(t, http.MethodPost, fmt.Sprintf("/v1/apps/%s/workflows/process-order/runs", app.Slug), map[string]any{}, map[string]string{"Idempotency-Key": "   "})
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	active, err := e.store.CountActiveRunsByApp(context.Background(), app.ID)
	if err != nil || active != 0 {
		t.Fatalf("active runs after invalid key = %d, err=%v; want 0", active, err)
	}
}

func TestCreateWorkflowRun_RuntimeDisabledRejectsBeforePersistence(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "runtime-disabled-app")
	e.s.WithWorkflowRuntimeEnabled(false)

	rec := e.do(t, "POST", fmt.Sprintf("/v1/apps/%s/workflows/process-order/runs", app.Slug), map[string]any{
		"order_id": "ord_disabled",
	}, nil)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 Not Implemented, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), api.CodeWorkflowDeploymentUnavailable) {
		t.Fatalf("expected error code %s in body: %s", api.CodeWorkflowDeploymentUnavailable, rec.Body.String())
	}
	active, err := e.store.CountActiveRunsByApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("CountActiveRunsByApp: %v", err)
	}
	if active != 0 {
		t.Fatalf("active workflow runs = %d, want 0 after runtime rejection", active)
	}
}

func TestCreateWorkflowRun_TenantRequiredAppRejectsBeforePersistence(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "tenant-workflow-app")
	required := true
	if _, err := e.store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		PlatformTenantRequired:    &required,
		SetPlatformTenantRequired: true,
	}); err != nil {
		t.Fatalf("mark app as tenant-required: %v", err)
	}

	rec := e.do(t, "POST", fmt.Sprintf("/v1/apps/%s/workflows/process-order/runs", app.Slug), map[string]any{
		"order_id": "ord_tenant",
	}, nil)
	assertProblem(t, rec, http.StatusConflict, api.CodeWorkflowTenantIdentityUnavailable)

	_, total, err := e.store.ListWorkflowRuns(context.Background(), app.ID, state.ListWorkflowRunsOpts{})
	if err != nil {
		t.Fatalf("ListWorkflowRuns: %v", err)
	}
	if total != 0 {
		t.Fatalf("persisted workflow runs = %d, want 0 after tenant identity rejection", total)
	}
}

func TestCreateTenantWorkflowRun_PersistsVerifiedTenantAndRejectsUnsupportedFeatures(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "tenant-scoped-workflow-app")
	required := true
	if _, err := e.store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		PlatformTenantRequired: &required, SetPlatformTenantRequired: true,
	}); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "workflow-customer", "Workflow customer", 10)
	if err != nil {
		t.Fatal(err)
	}
	seedPlatformTenantConsumer(t, e, app.Slug, tenant.ID)

	issued := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", api.CreatePlatformTenantAccessTokenRequest{
		Name: "workflow starter", Scopes: []string{api.ScopePlatformTenantInvocationsManage, api.ScopePlatformTenantInvocationsRead},
	}, nil)
	if issued.Code != http.StatusCreated {
		t.Fatalf("create tenant token: %d %s", issued.Code, issued.Body)
	}
	var token api.CreatePlatformTenantAccessTokenResponse
	if err := json.Unmarshal(issued.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}

	started := e.do(t, http.MethodPost, "/v1/platform-tenant-self/apps/"+app.Slug+"/workflows/process-order/runs", map[string]any{"order_id": "ord_42"}, map[string]string{
		"Authorization": "Bearer " + token.Token,
	})
	if started.Code != http.StatusCreated {
		t.Fatalf("tenant self start: %d %s", started.Code, started.Body)
	}
	var response api.WorkflowRunResponse
	if err := json.Unmarshal(started.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.PlatformTenantID != tenant.ID {
		t.Fatalf("response tenant_id=%q, want %q", response.PlatformTenantID, tenant.ID)
	}
	stored, err := e.store.GetWorkflowRun(context.Background(), response.ID)
	if err != nil || stored.PlatformTenantID != tenant.ID {
		t.Fatalf("stored tenant run=%+v err=%v", stored, err)
	}
	managedStarted := e.do(t, http.MethodPost, "/v1/platform-tenant-self/apps/"+app.Slug+"/workflows/fulfill-transaction/runs", map[string]any{"order_id": "ord_44"}, map[string]string{
		"Authorization": "Bearer " + token.Token,
	})
	if managedStarted.Code != http.StatusCreated {
		t.Fatalf("tenant managed-operation workflow start: %d %s", managedStarted.Code, managedStarted.Body)
	}
	var managedRun api.WorkflowRunResponse
	if err := json.Unmarshal(managedStarted.Body.Bytes(), &managedRun); err != nil || managedRun.PlatformTenantID != tenant.ID {
		t.Fatalf("tenant managed-operation response=%+v err=%v", managedRun, err)
	}
	managedSnapshot, err := e.store.GetWorkflowRun(context.Background(), managedRun.ID)
	if err != nil || !strings.Contains(string(managedSnapshot.DefinitionSnapshot), `"managed_operation":true`) {
		t.Fatalf("tenant managed-operation snapshot=%s err=%v", managedSnapshot.DefinitionSnapshot, err)
	}
	selfPath := "/v1/platform-tenant-self/workflows/runs/" + response.ID
	status := e.do(t, http.MethodGet, selfPath, nil, map[string]string{"Authorization": "Bearer " + token.Token})
	if status.Code != http.StatusOK || status.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("tenant workflow status: %d %s", status.Code, status.Body)
	}
	var selfRun api.WorkflowRunResponse
	if err := json.Unmarshal(status.Body.Bytes(), &selfRun); err != nil || selfRun.ID != response.ID || selfRun.PlatformTenantID != tenant.ID {
		t.Fatalf("tenant workflow status response=%+v err=%v", selfRun, err)
	}

	unsupported := e.do(t, http.MethodPost, "/v1/platform-tenant-self/apps/"+app.Slug+"/workflows/approval/runs", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + token.Token,
	})
	if unsupported.Code != http.StatusBadRequest || !strings.Contains(unsupported.Body.String(), "event waits") {
		t.Fatalf("tenant event wait status=%d body=%s", unsupported.Code, unsupported.Body)
	}

	accountStarted := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/apps/"+app.Slug+"/workflows/process-order/runs", map[string]any{"order_id": "ord_43"}, nil)
	if accountStarted.Code != http.StatusCreated {
		t.Fatalf("account tenant start: %d %s", accountStarted.Code, accountStarted.Body)
	}
	var accountRun api.WorkflowRunResponse
	if err := json.Unmarshal(accountStarted.Body.Bytes(), &accountRun); err != nil || accountRun.PlatformTenantID != tenant.ID {
		t.Fatalf("account tenant response=%+v err=%v", accountRun, err)
	}
	cancelled := e.do(t, http.MethodPost, "/v1/platform-tenant-self/workflows/runs/"+accountRun.ID+"/cancel", nil, map[string]string{
		"Authorization": "Bearer " + token.Token,
	})
	var cancelledRun api.WorkflowRunResponse
	if err := json.Unmarshal(cancelled.Body.Bytes(), &cancelledRun); err != nil || cancelled.Code != http.StatusOK || cancelledRun.Status != state.WorkflowRunStatusFailed {
		t.Fatalf("tenant cancel: status=%d run=%+v err=%v body=%s", cancelled.Code, cancelledRun, err, cancelled.Body)
	}

	other, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "unlinked-workflow-customer", "Unlinked workflow customer", 10)
	if err != nil {
		t.Fatal(err)
	}
	otherIssued := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+other.ID+"/access-tokens", api.CreatePlatformTenantAccessTokenRequest{
		Name: "other workflow starter", Scopes: []string{api.ScopePlatformTenantInvocationsManage, api.ScopePlatformTenantInvocationsRead},
	}, nil)
	if otherIssued.Code != http.StatusCreated {
		t.Fatalf("create other tenant token: %d %s", otherIssued.Code, otherIssued.Body)
	}
	var otherToken api.CreatePlatformTenantAccessTokenResponse
	if err := json.Unmarshal(otherIssued.Body.Bytes(), &otherToken); err != nil {
		t.Fatal(err)
	}
	foreignRead := e.do(t, http.MethodGet, "/v1/platform-tenant-self/workflows/runs/"+accountRun.ID, nil, map[string]string{
		"Authorization": "Bearer " + otherToken.Token,
	})
	assertProblem(t, foreignRead, http.StatusNotFound, api.CodeWorkflowRunNotFound)
	foreign := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+other.ID+"/apps/"+app.Slug+"/workflows/process-order/runs", map[string]any{}, nil)
	assertProblem(t, foreign, http.StatusNotFound, api.CodeWorkflowDefinitionNotFound)
}

func TestListWorkflowRuns_And_GetWorkflowRun(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "list-app")

	// Create 2 runs
	_ = e.do(t, "POST", fmt.Sprintf("/v1/apps/%s/workflows/w1/runs", app.Slug), map[string]any{"i": 1}, nil)
	rec2 := e.do(t, "POST", fmt.Sprintf("/v1/apps/%s/workflows/w1/runs", app.Slug), map[string]any{"i": 2}, nil)

	var run2 api.WorkflowRunResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &run2)

	// List
	listRec := e.do(t, "GET", fmt.Sprintf("/v1/apps/%s/workflows/runs", app.Slug), nil, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list expected 200, got %d; body=%s", listRec.Code, listRec.Body.String())
	}

	var listResp api.ListWorkflowRunsResponse
	_ = json.Unmarshal(listRec.Body.Bytes(), &listResp)
	if listResp.Total != 2 || len(listResp.Runs) != 2 {
		t.Fatalf("expected 2 runs, got total=%d len=%d", listResp.Total, len(listResp.Runs))
	}

	// Get specific run
	getRec := e.do(t, "GET", fmt.Sprintf("/v1/workflows/runs/%s", run2.ID), nil, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get expected 200, got %d; body=%s", getRec.Code, getRec.Body.String())
	}
	var getResp api.WorkflowRunResponse
	_ = json.Unmarshal(getRec.Body.Bytes(), &getResp)
	if getResp.ID != run2.ID {
		t.Fatalf("expected run ID %s, got %s", run2.ID, getResp.ID)
	}
}

func TestListWorkflowRuns_RejectsInvalidStatus(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "list-status-app")
	rec := e.do(t, "GET", fmt.Sprintf("/v1/apps/%s/workflows/runs?status=nonsense", app.Slug), nil, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}

func TestListWorkflowRuns_FiltersAndValidatesCreatedRange(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "list-filters-app")
	for _, workflow := range []string{"w1", "approval", "w1"} {
		created := e.do(t, http.MethodPost, fmt.Sprintf("/v1/apps/%s/workflows/%s/runs", app.Slug, workflow), map[string]any{}, nil)
		if created.Code != http.StatusCreated {
			t.Fatalf("create %s run = %d: %s", workflow, created.Code, created.Body.String())
		}
	}

	filtered := e.do(t, http.MethodGet, fmt.Sprintf("/v1/apps/%s/workflows/runs?workflow_name=w1&created_after=1970-01-01T00:00:00Z&created_before=9999-12-31T23:59:59Z", app.Slug), nil, nil)
	if filtered.Code != http.StatusOK {
		t.Fatalf("filtered list = %d: %s", filtered.Code, filtered.Body.String())
	}
	var response api.ListWorkflowRunsResponse
	if err := json.Unmarshal(filtered.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 2 || len(response.Runs) != 2 || response.Runs[0].WorkflowName != "w1" || response.Runs[1].WorkflowName != "w1" {
		t.Fatalf("filtered workflow run response = %#v", response)
	}

	invalidTimestamp := e.do(t, http.MethodGet, fmt.Sprintf("/v1/apps/%s/workflows/runs?created_after=yesterday", app.Slug), nil, nil)
	assertProblem(t, invalidTimestamp, http.StatusBadRequest, api.CodeValidation)
	reversedRange := e.do(t, http.MethodGet, fmt.Sprintf("/v1/apps/%s/workflows/runs?created_after=2026-10-02T00:00:00Z&created_before=2026-10-01T00:00:00Z", app.Slug), nil, nil)
	assertProblem(t, reversedRange, http.StatusBadRequest, api.CodeValidation)
	emptyName := e.do(t, http.MethodGet, fmt.Sprintf("/v1/apps/%s/workflows/runs?workflow_name=", app.Slug), nil, nil)
	assertProblem(t, emptyName, http.StatusBadRequest, api.CodeValidation)
}

func TestWorkflowSteps_Events_And_Cancel(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "steps-app")

	createRec := e.do(t, "POST", fmt.Sprintf("/v1/apps/%s/workflows/approval/runs", app.Slug), map[string]any{"req": true}, nil)
	var run api.WorkflowRunResponse
	_ = json.Unmarshal(createRec.Body.Bytes(), &run)

	// Add steps to store
	_ = e.store.CreateWorkflowSteps(context.Background(), run.ID, []*state.WorkflowStep{
		{StepName: "step_one", Status: state.WorkflowStepStatusAwaitingEvent, Attempt: 1},
	})
	_ = e.store.MarkWorkflowRunStatus(context.Background(), run.ID, state.WorkflowRunStatusAwaitingEvent, nil, nil)

	// List steps
	stepsRec := e.do(t, "GET", fmt.Sprintf("/v1/workflows/runs/%s/steps", run.ID), nil, nil)
	if stepsRec.Code != http.StatusOK {
		t.Fatalf("steps expected 200, got %d; body=%s", stepsRec.Code, stepsRec.Body.String())
	}
	var stepsResp api.ListWorkflowStepsResponse
	_ = json.Unmarshal(stepsRec.Body.Bytes(), &stepsResp)
	if len(stepsResp.Steps) != 1 || stepsResp.Steps[0].StepName != "step_one" {
		t.Fatalf("expected step_one, got %+v", stepsResp.Steps)
	}

	// An unrelated event is recorded but must not complete the parked step.
	unrelatedRec := e.do(t, "POST", fmt.Sprintf("/v1/workflows/runs/%s/events", run.ID), api.InjectWorkflowEventRequest{
		EventName: "manager.rejected",
		Payload:   json.RawMessage(`{"approved":false}`),
	}, nil)
	if unrelatedRec.Code != http.StatusOK {
		t.Fatalf("unrelated event expected 200, got %d; body=%s", unrelatedRec.Code, unrelatedRec.Body.String())
	}
	parkedSteps, _ := e.store.GetWorkflowSteps(context.Background(), run.ID)
	if parkedSteps[0].Status != state.WorkflowStepStatusAwaitingEvent {
		t.Fatalf("unrelated event changed step status to %s", parkedSteps[0].Status)
	}

	// Inject the event the step actually waits for.
	eventRec := e.do(t, "POST", fmt.Sprintf("/v1/workflows/runs/%s/events", run.ID), api.InjectWorkflowEventRequest{
		EventName: "manager.approved",
		Payload:   json.RawMessage(`{"approved":true}`),
	}, nil)
	if eventRec.Code != http.StatusOK {
		t.Fatalf("event expected 200, got %d; body=%s", eventRec.Code, eventRec.Body.String())
	}

	// Cancel run
	cancelRec := e.do(t, "POST", fmt.Sprintf("/v1/workflows/runs/%s/cancel", run.ID), nil, nil)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel expected 200, got %d; body=%s", cancelRec.Code, cancelRec.Body.String())
	}
	var cancelResp api.WorkflowRunResponse
	_ = json.Unmarshal(cancelRec.Body.Bytes(), &cancelResp)
	if cancelResp.Status != state.WorkflowRunStatusFailed {
		t.Fatalf("expected cancelled status failed, got %s", cancelResp.Status)
	}
}

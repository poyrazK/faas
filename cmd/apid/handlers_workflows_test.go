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

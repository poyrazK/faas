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
	return app
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

	// Inject event
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

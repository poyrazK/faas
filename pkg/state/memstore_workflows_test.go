package state_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStore_WorkflowRunLifecycle(t *testing.T) {
	ctx := context.Background()
	ms := state.NewMemStore()

	// 1. Create run
	run := &state.WorkflowRun{
		AppID:              "app-1001",
		WorkflowName:       "user_signup",
		Input:              json.RawMessage(`{"user_id":"u1"}`),
		DefinitionSnapshot: json.RawMessage(`{"steps":[{"name":"verify"}]}`),
	}
	if err := ms.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	if run.ID == "" {
		t.Fatal("expected run.ID to be populated")
	}
	if run.Status != state.WorkflowRunStatusPending {
		t.Fatalf("expected pending status, got %s", run.Status)
	}

	// 2. Get run
	got, err := ms.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if got.WorkflowName != "user_signup" {
		t.Fatalf("expected user_signup, got %s", got.WorkflowName)
	}

	// 3. Count active runs
	count, err := ms.CountActiveRunsByApp(ctx, "app-1001")
	if err != nil {
		t.Fatalf("CountActiveRunsByApp: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 active run, got %d", count)
	}

	// 4. Claim next pending run
	claimed, err := ms.ClaimNextPendingRun(ctx)
	if err != nil {
		t.Fatalf("ClaimNextPendingRun: %v", err)
	}
	if claimed.ID != run.ID {
		t.Fatalf("expected claimed ID %s, got %s", run.ID, claimed.ID)
	}
	if claimed.Status != state.WorkflowRunStatusRunning {
		t.Fatalf("expected claimed status running, got %s", claimed.Status)
	}

	// 5. Claim again — should return ErrNotFound (no more pending runs)
	_, err = ms.ClaimNextPendingRun(ctx)
	if err != state.ErrNotFound {
		t.Fatalf("expected ErrNotFound on second claim, got %v", err)
	}

	// 6. Transition to succeeded
	out := json.RawMessage(`{"result":"ok"}`)
	if err := ms.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, out, nil); err != nil {
		t.Fatalf("MarkWorkflowRunStatus: %v", err)
	}

	got, err = ms.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if got.Status != state.WorkflowRunStatusSucceeded {
		t.Fatalf("expected succeeded status, got %s", got.Status)
	}
	if got.FinishedAt == nil {
		t.Fatal("expected FinishedAt to be populated")
	}

	// 7. Active count should now be 0
	count, err = ms.CountActiveRunsByApp(ctx, "app-1001")
	if err != nil {
		t.Fatalf("CountActiveRunsByApp: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 active runs, got %d", count)
	}
}

func TestMemStore_WorkflowStepsAndEvents(t *testing.T) {
	ctx := context.Background()
	ms := state.NewMemStore()

	run := &state.WorkflowRun{
		AppID:              "app-1002",
		WorkflowName:       "order_flow",
		DefinitionSnapshot: json.RawMessage(`{}`),
	}
	if err := ms.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}

	// 1. Create steps
	steps := []*state.WorkflowStep{
		{StepName: "charge"},
		{StepName: "ship"},
	}
	if err := ms.CreateWorkflowSteps(ctx, run.ID, steps); err != nil {
		t.Fatalf("CreateWorkflowSteps: %v", err)
	}

	// 2. Get steps
	gotSteps, err := ms.GetWorkflowSteps(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetWorkflowSteps: %v", err)
	}
	if len(gotSteps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(gotSteps))
	}

	// 3. Mark step status
	stepOut := json.RawMessage(`{"tx_id":"tx123"}`)
	if err := ms.MarkWorkflowStepStatus(ctx, run.ID, "charge", state.WorkflowStepStatusSucceeded, 1, stepOut, nil); err != nil {
		t.Fatalf("MarkWorkflowStepStatus: %v", err)
	}

	gotSteps, _ = ms.GetWorkflowSteps(ctx, run.ID)
	var chargeStep *state.WorkflowStep
	for _, s := range gotSteps {
		if s.StepName == "charge" {
			chargeStep = s
		}
	}
	if chargeStep == nil || chargeStep.Status != state.WorkflowStepStatusSucceeded {
		t.Fatalf("expected charge step succeeded, got %+v", chargeStep)
	}

	// 4. Events
	evt := &state.WorkflowEvent{
		RunID:     run.ID,
		EventName: "payment_confirmed",
		Payload:   json.RawMessage(`{"amount":100}`),
	}
	if err := ms.InsertWorkflowEvent(ctx, evt); err != nil {
		t.Fatalf("InsertWorkflowEvent: %v", err)
	}

	matched, err := ms.FindMatchingEvent(ctx, run.ID, "payment_confirmed")
	if err != nil {
		t.Fatalf("FindMatchingEvent: %v", err)
	}
	if matched.EventName != "payment_confirmed" {
		t.Fatalf("expected payment_confirmed, got %s", matched.EventName)
	}

	// 5. Event not found
	_, err = ms.FindMatchingEvent(ctx, run.ID, "non_existent")
	if err != state.ErrWorkflowEventNotFound {
		t.Fatalf("expected ErrWorkflowEventNotFound, got %v", err)
	}
}

func TestMemStore_WorkflowListPagination(t *testing.T) {
	ctx := context.Background()
	ms := state.NewMemStore()

	for i := 0; i < 5; i++ {
		r := &state.WorkflowRun{
			AppID:              "app-1003",
			WorkflowName:       "loop",
			DefinitionSnapshot: json.RawMessage(`{}`),
		}
		if err := ms.CreateWorkflowRun(ctx, r); err != nil {
			t.Fatalf("CreateWorkflowRun: %v", err)
		}
	}

	// List with limit 2
	runs, total, err := ms.ListWorkflowRuns(ctx, "app-1003", state.ListWorkflowRunsOpts{Limit: 2})
	if err != nil {
		t.Fatalf("ListWorkflowRuns: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected total 5, got %d", total)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 items, got %d", len(runs))
	}
}

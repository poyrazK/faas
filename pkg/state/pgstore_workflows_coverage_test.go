package state_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStore_WorkflowsCoverage(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)

	// Create account and app for foreign key relations
	acct, err := s.CreateAccount(ctx, "wf-pg-test@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID,
		Slug:      "wf-pg-app",
		RAMMB:     256,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// 1. Create run
	run := &state.WorkflowRun{
		AppID:              app.ID,
		WorkflowName:       "pg_workflow",
		Input:              json.RawMessage(`{"key":"val"}`),
		DefinitionSnapshot: json.RawMessage(`{"steps":[{"name":"s1"}]}`),
	}
	if err := s.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}

	// 2. Get run
	gotRun, err := s.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if gotRun.WorkflowName != "pg_workflow" {
		t.Fatalf("expected pg_workflow, got %s", gotRun.WorkflowName)
	}

	// 3. Count active runs
	activeCount, err := s.CountActiveRunsByApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("CountActiveRunsByApp: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("expected 1 active run, got %d", activeCount)
	}

	// 4. List runs
	runs, total, err := s.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{})
	if err != nil {
		t.Fatalf("ListWorkflowRuns: %v", err)
	}
	if total != 1 || len(runs) != 1 {
		t.Fatalf("expected 1 run, got total=%d len=%d", total, len(runs))
	}

	// 5. Claim next pending run
	claimed, err := s.ClaimNextPendingRun(ctx)
	if err != nil {
		t.Fatalf("ClaimNextPendingRun: %v", err)
	}
	if claimed.ID != run.ID {
		t.Fatalf("expected claimed ID %s, got %s", run.ID, claimed.ID)
	}
	if claimed.Status != state.WorkflowRunStatusRunning {
		t.Fatalf("expected running, got %s", claimed.Status)
	}

	// 6. Steps
	steps := []*state.WorkflowStep{
		{StepName: "step_one", Attempt: 0},
	}
	if err := s.CreateWorkflowSteps(ctx, run.ID, steps); err != nil {
		t.Fatalf("CreateWorkflowSteps: %v", err)
	}

	gotSteps, err := s.GetWorkflowSteps(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetWorkflowSteps: %v", err)
	}
	if len(gotSteps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(gotSteps))
	}

	stepOut := json.RawMessage(`{"out":true}`)
	if err := s.MarkWorkflowStepStatus(ctx, run.ID, "step_one", state.WorkflowStepStatusSucceeded, 1, stepOut, nil); err != nil {
		t.Fatalf("MarkWorkflowStepStatus: %v", err)
	}

	// 7. Events
	evt := &state.WorkflowEvent{
		RunID:     run.ID,
		EventName: "evt_ping",
		Payload:   json.RawMessage(`{"ping":1}`),
	}
	if err := s.InsertWorkflowEvent(ctx, evt); err != nil {
		t.Fatalf("InsertWorkflowEvent: %v", err)
	}

	evts, err := s.GetWorkflowEventsForRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetWorkflowEventsForRun: %v", err)
	}
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}

	foundEvt, err := s.FindMatchingEvent(ctx, run.ID, "evt_ping")
	if err != nil {
		t.Fatalf("FindMatchingEvent: %v", err)
	}
	if foundEvt.EventName != "evt_ping" {
		t.Fatalf("expected evt_ping, got %s", foundEvt.EventName)
	}

	// 8. Mark run completed
	runOut := json.RawMessage(`{"final":true}`)
	if err := s.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, runOut, nil); err != nil {
		t.Fatalf("MarkWorkflowRunStatus: %v", err)
	}

	// 9. Sweep expired
	deletedRuns, err := s.SweepExpiredWorkflowRuns(ctx, 0)
	if err != nil {
		t.Fatalf("SweepExpiredWorkflowRuns: %v", err)
	}
	if deletedRuns != 1 {
		t.Fatalf("expected 1 deleted run, got %d", deletedRuns)
	}

	deletedEvts, err := s.SweepExpiredWorkflowEvents(ctx, 0)
	if err != nil {
		t.Fatalf("SweepExpiredWorkflowEvents: %v", err)
	}
	_ = deletedEvts
}

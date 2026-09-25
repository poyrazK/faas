package state_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStore_WorkflowCallbackAndEventParking(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "wf-callback@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "wf-callback", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "callback", DefinitionSnapshot: json.RawMessage("{\"name\":\"callback\"}")}
	if err := s.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	callbackID := api.WorkflowCallbackID(run.ID, "await")
	eventName := api.WorkflowCallbackEventName(run.ID, "await")
	payload := json.RawMessage("{\"ok\":true}")
	if duplicate, err := s.CompleteWorkflowCallback(ctx, run.ID, "await", eventName, callbackID, time.Hour, payload); err != nil || duplicate {
		t.Fatalf("early callback: duplicate=%t err=%v", duplicate, err)
	}
	if _, err := s.SweepExpiredWorkflowEvents(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "await"}}); err != nil {
		t.Fatal(err)
	}
	if event, _, err := s.ParkWorkflowEvent(ctx, run.ID, "await", eventName, time.Hour); err != nil || event == nil {
		t.Fatalf("early callback lost: event=%v err=%v", event, err)
	}
	if duplicate, err := s.CompleteWorkflowCallback(ctx, run.ID, "await", eventName, callbackID, time.Hour, payload); err != nil || !duplicate {
		t.Fatalf("duplicate callback: duplicate=%t err=%v", duplicate, err)
	}
	if _, err := s.CompleteWorkflowCallback(ctx, run.ID, "await", eventName, callbackID, time.Hour, json.RawMessage("{\"ok\":false}")); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed callback payload = %v", err)
	}

	race := &state.WorkflowRun{AppID: app.ID, WorkflowName: "race", DefinitionSnapshot: json.RawMessage("{\"name\":\"race\"}")}
	if err := s.CreateWorkflowRun(ctx, race); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkflowSteps(ctx, race.ID, []*state.WorkflowStep{{StepName: "await"}}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var parkedEvent *state.WorkflowEvent
	var parkErr, insertErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		parkedEvent, _, parkErr = s.ParkWorkflowEvent(ctx, race.ID, "await", "ready", time.Hour)
	}()
	go func() {
		defer wg.Done()
		<-start
		insertErr = s.InsertWorkflowEvent(ctx, &state.WorkflowEvent{RunID: race.ID, EventName: "ready", Payload: payload})
	}()
	close(start)
	wg.Wait()
	if parkErr != nil || insertErr != nil {
		t.Fatalf("concurrent park=%v insert=%v", parkErr, insertErr)
	}
	stored, err := s.GetWorkflowRun(ctx, race.ID)
	if err != nil {
		t.Fatal(err)
	}
	if parkedEvent == nil && stored.Status != state.WorkflowRunStatusPending {
		t.Fatalf("arrival lost while parked: %#v", stored)
	}
}

func TestPgStore_WorkflowTimerDeadlineIsStable(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "wf-timer@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "wf-timer", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "timer", DefinitionSnapshot: json.RawMessage(`{"name":"timer","steps":[{"name":"wait","wait_for_duration":"1h"}]}`)}
	if err := s.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "wait"}}); err != nil {
		t.Fatal(err)
	}
	deadline, err := s.ParkWorkflowTimer(ctx, run.ID, "wait", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	parked, err := s.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if parked.Status != state.WorkflowRunStatusAwaitingEvent || !parked.ScheduledFor.Equal(deadline) {
		t.Fatalf("parked run = %#v, deadline = %v", parked, deadline)
	}
	steps, err := s.GetWorkflowSteps(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].StartedAt == nil || !steps[0].StartedAt.Add(time.Hour).Equal(deadline) {
		t.Fatalf("timer step = %#v, deadline = %v", steps, deadline)
	}
	if err := s.ScheduleWorkflowRun(ctx, run.ID, state.WorkflowRunStatusPending, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	reparked, err := s.ParkWorkflowTimer(ctx, run.ID, "wait", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !reparked.Equal(deadline) {
		t.Fatalf("reparked deadline = %v, want %v", reparked, deadline)
	}
	if _, err := s.CancelWorkflowRun(ctx, run.ID, "test cancellation"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ParkWorkflowTimer(ctx, run.ID, "wait", time.Hour); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("repark cancelled run = %v, want conflict", err)
	}
}

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

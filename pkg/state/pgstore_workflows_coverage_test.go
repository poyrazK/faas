package state_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStore_WorkflowCallbackWebhookBinding(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "wf-webhook-binding@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "wf-webhook-binding", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := s.CreateInboundWebhookEndpointIfUnderQuota(ctx, state.InboundWebhookEndpoint{
		AppID: app.ID, AccountID: acct.ID, Name: "stripe",
		Provider: state.InboundWebhookProviderStripe, TokenHash: bytes.Repeat([]byte{1}, 32),
		SigningSecretSealed: []byte{1}, DeliveryPath: "/", Enabled: true,
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "callback", DefinitionSnapshot: json.RawMessage("{\"name\":\"callback\"}")}
	if err := s.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	binding := state.WorkflowCallbackWebhookBinding{
		ID: api.WorkflowCallbackID(run.ID, "await"), EndpointID: endpoint.ID,
		RunID: run.ID, StepName: "await", EventType: "payment_intent.succeeded", ObjectID: "pi_1",
	}
	created, err := s.CreateWorkflowCallbackWebhookBinding(ctx, binding)
	if err != nil || created.CreatedAt.IsZero() {
		t.Fatalf("create binding = %#v, err=%v", created, err)
	}
	if repeated, err := s.CreateWorkflowCallbackWebhookBinding(ctx, binding); err != nil || repeated.ID != created.ID {
		t.Fatalf("idempotent binding = %#v, err=%v", repeated, err)
	}
	changed := binding
	changed.ObjectID = "pi_2"
	if _, err := s.CreateWorkflowCallbackWebhookBinding(ctx, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed binding = %v", err)
	}
	matched, err := s.WorkflowCallbackWebhookBindingByMatch(ctx, endpoint.ID, binding.EventType, binding.ObjectID)
	if err != nil || matched.ID != binding.ID {
		t.Fatalf("match binding = %#v, err=%v", matched, err)
	}
	otherRun := &state.WorkflowRun{AppID: app.ID, WorkflowName: "callback", DefinitionSnapshot: json.RawMessage("{\"name\":\"callback\"}")}
	if err := s.CreateWorkflowRun(ctx, otherRun); err != nil {
		t.Fatal(err)
	}
	claimed := binding
	claimed.RunID = otherRun.ID
	claimed.ID = api.WorkflowCallbackID(otherRun.ID, claimed.StepName)
	if _, err := s.CreateWorkflowCallbackWebhookBinding(ctx, claimed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("same provider event claimed by second run = %v", err)
	}
	if _, err := s.WorkflowCallbackWebhookBindingByMatch(ctx, endpoint.ID, binding.EventType, "pi_other"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unmatched binding = %v", err)
	}
	if err := s.DeleteWorkflowCallbackWebhookBinding(ctx, binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WorkflowCallbackWebhookBindingByID(ctx, binding.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted binding = %v", err)
	}
}

func TestPgStore_WorkflowConditionCheckTransitions(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "wf-condition@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "wf-condition", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "condition", DefinitionSnapshot: json.RawMessage(`{"name":"condition"}`)}
	if err := s.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "await"}}); err != nil {
		t.Fatal(err)
	}
	u := state.WorkflowConditionUpdate{RunID: run.ID, StepName: "await", Interval: 80 * time.Millisecond, Timeout: time.Second, MaxAttempts: 3}
	if got, err := s.ResolveWorkflowCondition(ctx, u); err != nil || got.Status != state.WorkflowConditionReady {
		t.Fatalf("initial condition = %#v, %v", got, err)
	}
	if err := s.MarkWorkflowStepStatus(ctx, run.ID, "await", state.WorkflowStepStatusRunning, 1, nil, nil); err != nil {
		t.Fatal(err)
	}
	u.Checked = true
	u.Result = json.RawMessage(`{"done":false,"state":{"id":"ord_1"}}`)
	first, err := s.ResolveWorkflowCondition(ctx, u)
	if err != nil || first.Status != state.WorkflowConditionWaiting || first.NextCheckAt.IsZero() {
		t.Fatalf("first check = %#v, %v", first, err)
	}
	steps, err := s.GetWorkflowSteps(ctx, run.ID)
	if err != nil || len(steps) != 1 || steps[0].NextCheckAt == nil || !steps[0].NextCheckAt.Equal(first.NextCheckAt) || steps[0].Attempt != 1 {
		t.Fatalf("durable check state = %#v, %v", steps, err)
	}
	u.Checked = false
	u.Result = nil
	if got, err := s.ResolveWorkflowCondition(ctx, u); err != nil || got.Status != state.WorkflowConditionWaiting || !got.NextCheckAt.Equal(first.NextCheckAt) {
		t.Fatalf("early recheck = %#v, %v", got, err)
	}
	if delay := time.Until(first.NextCheckAt); delay > 0 {
		time.Sleep(delay + 10*time.Millisecond)
	}
	if got, err := s.ResolveWorkflowCondition(ctx, u); err != nil || got.Status != state.WorkflowConditionReady {
		t.Fatalf("due check = %#v, %v", got, err)
	}
	if err := s.MarkWorkflowStepStatus(ctx, run.ID, "await", state.WorkflowStepStatusRunning, 2, nil, nil); err != nil {
		t.Fatal(err)
	}
	u.Checked, u.Done = true, true
	u.Result = json.RawMessage(`{"done":true,"result":"delivered"}`)
	if got, err := s.ResolveWorkflowCondition(ctx, u); err != nil || got.Status != state.WorkflowConditionSucceeded {
		t.Fatalf("completed check = %#v, %v", got, err)
	}
	steps, err = s.GetWorkflowSteps(ctx, run.ID)
	if err != nil || steps[0].Status != state.WorkflowStepStatusSucceeded || steps[0].NextCheckAt != nil || !json.Valid(steps[0].Output) {
		t.Fatalf("completed step = status:%s next:%v output:%s err:%v", steps[0].Status, steps[0].NextCheckAt, steps[0].Output, err)
	}
	var completed map[string]any
	if err := json.Unmarshal(steps[0].Output, &completed); err != nil || completed["done"] != true || completed["result"] != "delivered" {
		t.Fatalf("completed output = %s, %v", steps[0].Output, err)
	}

	other := &state.WorkflowRun{AppID: app.ID, WorkflowName: "condition", DefinitionSnapshot: json.RawMessage(`{"name":"condition"}`)}
	if err := s.CreateWorkflowRun(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkflowSteps(ctx, other.ID, []*state.WorkflowStep{{StepName: "await"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkWorkflowStepStatus(ctx, other.ID, "await", state.WorkflowStepStatusRunning, 1, nil, nil); err != nil {
		t.Fatal(err)
	}
	u = state.WorkflowConditionUpdate{RunID: other.ID, StepName: "await", Checked: true, Result: json.RawMessage(`{"done":false}`), Interval: time.Hour, Timeout: time.Hour, MaxAttempts: 1, OnTimeout: true}
	if got, err := s.ResolveWorkflowCondition(ctx, u); err != nil || got.Status != state.WorkflowConditionTimedOut {
		t.Fatalf("attempt exhaustion = %#v, %v", got, err)
	}
	steps, err = s.GetWorkflowSteps(ctx, other.ID)
	var timedOut map[string]any
	if err != nil || json.Unmarshal(steps[0].Output, &timedOut) != nil || timedOut["timeout"] != true {
		t.Fatalf("timeout step = %s, %v", steps[0].Output, err)
	}

	late := &state.WorkflowRun{AppID: app.ID, WorkflowName: "condition", DefinitionSnapshot: json.RawMessage(`{"name":"condition"}`)}
	if err := s.CreateWorkflowRun(ctx, late); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkflowSteps(ctx, late.ID, []*state.WorkflowStep{{StepName: "await"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkWorkflowStepStatus(ctx, late.ID, "await", state.WorkflowStepStatusRunning, 1, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workflow_steps SET started_at = now() - interval '1 hour' WHERE run_id = $1 AND step_name = 'await'`, late.ID); err != nil {
		t.Fatal(err)
	}
	u = state.WorkflowConditionUpdate{RunID: late.ID, StepName: "await", Checked: true, Done: true, Result: json.RawMessage(`{"done":true}`), Interval: time.Minute, Timeout: time.Minute, MaxAttempts: 3}
	if got, err := s.ResolveWorkflowCondition(ctx, u); err != nil || got.Status != state.WorkflowConditionTimedOut {
		t.Fatalf("late completion = %#v, %v", got, err)
	}
	steps, err = s.GetWorkflowSteps(ctx, late.ID)
	if err != nil || steps[0].Status != state.WorkflowStepStatusDead {
		t.Fatalf("late completion step = %#v, %v", steps, err)
	}
}

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

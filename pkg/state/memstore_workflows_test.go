package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStore_WorkflowEventParkCannotLoseConcurrentArrival(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	for i := 0; i < 100; i++ {
		run := &state.WorkflowRun{AppID: "app-race", WorkflowName: "race", DefinitionSnapshot: json.RawMessage("{\"name\":\"race\"}")}
		if err := store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "await"}}); err != nil {
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
			parkedEvent, _, parkErr = store.ParkWorkflowEvent(ctx, run.ID, "await", "ready", time.Hour)
		}()
		go func() {
			defer wg.Done()
			<-start
			insertErr = store.InsertWorkflowEvent(ctx, &state.WorkflowEvent{RunID: run.ID, EventName: "ready", Payload: json.RawMessage("{\"ok\":true}")})
		}()
		close(start)
		wg.Wait()
		if parkErr != nil || insertErr != nil {
			t.Fatalf("iteration %d: park=%v insert=%v", i, parkErr, insertErr)
		}
		stored, err := store.GetWorkflowRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if parkedEvent == nil && stored.Status != state.WorkflowRunStatusPending {
			t.Fatalf("iteration %d: event was not seen and run stayed parked: %#v", i, stored)
		}
	}
}

func TestMemStore_WorkflowConditionDeadlineWinsLateCheckerResult(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	run := &state.WorkflowRun{AppID: "app-condition-deadline", WorkflowName: "condition", DefinitionSnapshot: json.RawMessage(`{"name":"condition"}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "await"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowStepStatus(ctx, run.ID, "await", state.WorkflowStepStatusRunning, 1, nil, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(15 * time.Millisecond)
	result, err := store.ResolveWorkflowCondition(ctx, state.WorkflowConditionUpdate{
		RunID: run.ID, StepName: "await", Checked: true, Done: true,
		Result: json.RawMessage(`{"done":true}`), Interval: time.Minute, Timeout: 5 * time.Millisecond, MaxAttempts: 3,
	})
	if err != nil || result.Status != state.WorkflowConditionTimedOut {
		t.Fatalf("late checker result = %#v, %v", result, err)
	}
	final, _ := store.GetWorkflowRun(ctx, run.ID)
	if final.Status != state.WorkflowRunStatusDead {
		t.Fatalf("late checker completed run: %s", final.Status)
	}
}

func TestMemStore_WorkflowCallbackEarlyDuplicateExpiryAndCancellation(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	run := &state.WorkflowRun{AppID: "app-callback", WorkflowName: "callback", DefinitionSnapshot: json.RawMessage("{\"name\":\"callback\"}")}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	id := "32f8849b-4b65-5557-a125-30da97c38a7b"
	eventName := "workflow.callback." + id
	payload := json.RawMessage("{\"a\":1,\"b\":2}")
	duplicate, err := store.CompleteWorkflowCallback(ctx, run.ID, "await", eventName, id, time.Hour, payload)
	if err != nil || duplicate {
		t.Fatalf("early callback: duplicate=%t err=%v", duplicate, err)
	}
	duplicate, err = store.CompleteWorkflowCallback(ctx, run.ID, "await", eventName, id, time.Hour, json.RawMessage("{\"b\":2,\"a\":1}"))
	if err != nil || !duplicate {
		t.Fatalf("duplicate callback: duplicate=%t err=%v", duplicate, err)
	}
	if _, err := store.CompleteWorkflowCallback(ctx, run.ID, "await", eventName, id, time.Hour, json.RawMessage("{\"a\":3}")); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed duplicate = %v, want conflict", err)
	}
	if _, err := store.CompleteWorkflowCallback(ctx, run.ID, "await", eventName, id, time.Hour, json.RawMessage("{\"n\":9007199254740993}")); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("different large-number payload = %v, want conflict", err)
	}
	if err := store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "await"}}); err != nil {
		t.Fatal(err)
	}
	event, _, err := store.ParkWorkflowEvent(ctx, run.ID, "await", eventName, time.Hour)
	if err != nil || event == nil || string(event.Payload) != string(payload) {
		t.Fatalf("early callback not visible at park: event=%#v err=%v", event, err)
	}
	if _, err := store.CancelWorkflowRun(ctx, run.ID, "test"); err != nil {
		t.Fatal(err)
	}
	duplicate, err = store.CompleteWorkflowCallback(ctx, run.ID, "await", eventName, id, time.Hour, payload)
	if err != nil || !duplicate {
		t.Fatalf("duplicate after cancel: duplicate=%t err=%v", duplicate, err)
	}

	expiring := &state.WorkflowRun{AppID: "app-callback", WorkflowName: "expiring", DefinitionSnapshot: json.RawMessage("{\"name\":\"expiring\"}")}
	if err := store.CreateWorkflowRun(ctx, expiring); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowSteps(ctx, expiring.ID, []*state.WorkflowStep{{StepName: "await"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ParkWorkflowEvent(ctx, expiring.ID, "await", "expiring.event", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := store.CompleteWorkflowCallback(ctx, expiring.ID, "await", "expiring.event", "e7570364-283a-50d4-8ac1-a8ad559fab8b", time.Millisecond, payload); !errors.Is(err, state.ErrWorkflowCallbackExpired) {
		t.Fatalf("expired callback = %v", err)
	}
	if _, err := store.CancelWorkflowRun(ctx, expiring.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteWorkflowCallback(ctx, expiring.ID, "await", "expiring.event", "e7570364-283a-50d4-8ac1-a8ad559fab8b", time.Millisecond, payload); !errors.Is(err, state.ErrWorkflowCallbackClosed) {
		t.Fatalf("cancelled callback = %v", err)
	}

	precision := &state.WorkflowRun{AppID: "app-callback", WorkflowName: "precision", DefinitionSnapshot: json.RawMessage("{\"name\":\"precision\"}")}
	if err := store.CreateWorkflowRun(ctx, precision); err != nil {
		t.Fatal(err)
	}
	precisionID := "eb9eff27-21fd-590d-b23c-3d596ac645b1"
	if _, err := store.CompleteWorkflowCallback(ctx, precision.ID, "await", "precision.event", precisionID, time.Hour, json.RawMessage("{\"n\":9007199254740992}")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteWorkflowCallback(ctx, precision.ID, "await", "precision.event", precisionID, time.Hour, json.RawMessage("{\"n\":9007199254740993}")); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("large-number payload conflict = %v", err)
	}
	number := &state.WorkflowRun{AppID: "app-callback", WorkflowName: "number", DefinitionSnapshot: json.RawMessage("{\"name\":\"number\"}")}
	if err := store.CreateWorkflowRun(ctx, number); err != nil {
		t.Fatal(err)
	}
	numberID := "55bb1d72-daa9-5ab0-a97a-caf5e672e31d"
	if _, err := store.CompleteWorkflowCallback(ctx, number.ID, "await", "number.event", numberID, time.Hour, json.RawMessage("{\"n\":1e0}")); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := store.CompleteWorkflowCallback(ctx, number.ID, "await", "number.event", numberID, time.Hour, json.RawMessage("{\"n\":1.0}")); err != nil || !duplicate {
		t.Fatalf("equivalent numeric payload: duplicate=%t err=%v", duplicate, err)
	}
}

func TestMemStore_WorkflowEventTimeoutResolutionAndRetention(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	run := &state.WorkflowRun{AppID: "app-timeout", WorkflowName: "timeout", DefinitionSnapshot: json.RawMessage("{\"name\":\"timeout\"}")}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "await"}}); err != nil {
		t.Fatal(err)
	}
	event := &state.WorkflowEvent{RunID: run.ID, EventName: "ready", Payload: json.RawMessage("{\"ok\":true}")}
	if err := store.InsertWorkflowEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if deleted, err := store.SweepExpiredWorkflowEvents(ctx, 0); err != nil || deleted != 0 {
		t.Fatalf("active-run event was swept: deleted=%d err=%v", deleted, err)
	}
	if matched, _, err := store.ParkWorkflowEvent(ctx, run.ID, "await", "ready", time.Millisecond); err != nil || matched == nil {
		t.Fatalf("early event vanished: matched=%v err=%v", matched, err)
	}

	expiring := &state.WorkflowRun{AppID: "app-timeout", WorkflowName: "timeout", DefinitionSnapshot: json.RawMessage("{\"name\":\"timeout\"}")}
	if err := store.CreateWorkflowRun(ctx, expiring); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowSteps(ctx, expiring.ID, []*state.WorkflowStep{{StepName: "await"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ParkWorkflowEvent(ctx, expiring.ID, "await", "late", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	if err := store.InsertWorkflowEvent(ctx, &state.WorkflowEvent{RunID: expiring.ID, EventName: "late", Payload: json.RawMessage("{\"too_late\":true}")}); err != nil {
		t.Fatal(err)
	}
	if matched, _, err := store.ParkWorkflowEvent(ctx, expiring.ID, "await", "late", time.Millisecond); err != nil || matched != nil {
		t.Fatalf("late event matched: matched=%v err=%v", matched, err)
	}
	matched, timedOut, err := store.ResolveWorkflowEventWait(ctx, expiring.ID, "await", "late", time.Millisecond, true)
	if err != nil || matched != nil || !timedOut {
		t.Fatalf("resolve timeout: matched=%v timedOut=%t err=%v", matched, timedOut, err)
	}
	steps, err := store.GetWorkflowSteps(ctx, expiring.ID)
	if err != nil || len(steps) != 1 || steps[0].Status != state.WorkflowStepStatusSucceeded || string(steps[0].Output) != "{\"timeout\":true}" {
		t.Fatalf("timeout step = %#v, err=%v", steps, err)
	}
}

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
	if !errors.Is(err, state.ErrNotFound) {
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

func TestMemStore_DueWorkflowRunScheduling(t *testing.T) {
	ctx := context.Background()
	ms := state.NewMemStore()
	future := time.Now().UTC().Add(time.Hour)
	run := &state.WorkflowRun{
		AppID: "app-due", WorkflowName: "due", ScheduledFor: future,
		DefinitionSnapshot: json.RawMessage(`{"steps":[{"name":"main"}]}`),
	}
	if err := ms.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	if _, err := ms.ClaimNextDueWorkflowRun(ctx); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("future claim error = %v, want ErrNotFound", err)
	}
	if err := ms.ScheduleWorkflowRun(ctx, run.ID, state.WorkflowRunStatusPending, time.Now().UTC()); err != nil {
		t.Fatalf("ScheduleWorkflowRun: %v", err)
	}
	claimed, err := ms.ClaimNextDueWorkflowRun(ctx)
	if err != nil {
		t.Fatalf("ClaimNextDueWorkflowRun: %v", err)
	}
	if claimed.ID != run.ID || claimed.Status != state.WorkflowRunStatusRunning {
		t.Fatalf("claimed = %#v, want running %s", claimed, run.ID)
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
	if !errors.Is(err, state.ErrWorkflowEventNotFound) {
		t.Fatalf("expected ErrWorkflowEventNotFound, got %v", err)
	}
}

func TestMemStore_WorkflowEventsMatchOldestAndRejectDuplicateID(t *testing.T) {
	ctx := context.Background()
	ms := state.NewMemStore()
	run := &state.WorkflowRun{
		AppID:              "app-event-order",
		WorkflowName:       "event-order",
		DefinitionSnapshot: json.RawMessage(`{}`),
	}
	if err := ms.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	now := time.Now().UTC()
	older := &state.WorkflowEvent{
		ID: "event-older", RunID: run.ID, EventName: "ready",
		Payload: json.RawMessage(`{"source":"older"}`), ReceivedAt: now.Add(-time.Minute),
	}
	newer := &state.WorkflowEvent{
		ID: "event-newer", RunID: run.ID, EventName: "ready",
		Payload: json.RawMessage(`{"source":"newer"}`), ReceivedAt: now,
	}
	if err := ms.InsertWorkflowEvent(ctx, newer); err != nil {
		t.Fatalf("InsertWorkflowEvent newer: %v", err)
	}
	if err := ms.InsertWorkflowEvent(ctx, older); err != nil {
		t.Fatalf("InsertWorkflowEvent older: %v", err)
	}
	matched, err := ms.FindMatchingEvent(ctx, run.ID, "ready")
	if err != nil {
		t.Fatalf("FindMatchingEvent: %v", err)
	}
	if matched.ID != older.ID {
		t.Fatalf("matched event = %q, want oldest %q", matched.ID, older.ID)
	}
	if err := ms.InsertWorkflowEvent(ctx, &state.WorkflowEvent{
		ID: "event-older", RunID: run.ID, EventName: "duplicate",
		Payload: json.RawMessage(`{}`),
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate event ID error = %v, want ErrConflict", err)
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

func TestMemStore_WorkflowAdmissionRecoveryAndCancel(t *testing.T) {
	ctx := context.Background()
	ms := state.NewMemStore()

	run := &state.WorkflowRun{
		AppID:              "app-admission",
		WorkflowName:       "recoverable",
		DefinitionSnapshot: json.RawMessage(`{"steps":["first","second"]}`),
	}
	if _, err := ms.CreateWorkflowRunAdmitted(ctx, run, 0); !errors.Is(err, state.ErrWorkflowRunQuotaExceeded) {
		t.Fatalf("zero quota error = %v, want ErrWorkflowRunQuotaExceeded", err)
	}
	active, err := ms.CreateWorkflowRunAdmitted(ctx, run, 1)
	if err != nil || active != 1 {
		t.Fatalf("CreateWorkflowRunAdmitted = (%d, %v), want (1, nil)", active, err)
	}

	overQuota := &state.WorkflowRun{
		AppID:              run.AppID,
		WorkflowName:       "over-quota",
		DefinitionSnapshot: json.RawMessage(`{}`),
	}
	if active, err := ms.CreateWorkflowRunAdmitted(ctx, overQuota, 1); active != 1 || !errors.Is(err, state.ErrWorkflowRunQuotaExceeded) {
		t.Fatalf("over-quota admission = (%d, %v), want (1, ErrWorkflowRunQuotaExceeded)", active, err)
	}

	if err := ms.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{
		{StepName: "first", Status: state.WorkflowStepStatusRunning, Attempt: 2},
		{StepName: "second", Status: state.WorkflowStepStatusPending},
	}); err != nil {
		t.Fatalf("CreateWorkflowSteps: %v", err)
	}
	if err := ms.RecoverWorkflowRun(ctx, run.ID); err != nil {
		t.Fatalf("RecoverWorkflowRun: %v", err)
	}
	steps, err := ms.GetWorkflowSteps(ctx, run.ID)
	if err != nil || len(steps) != 2 || steps[0].Status != state.WorkflowStepStatusPending || steps[0].Attempt != 1 {
		t.Fatalf("recovered steps = (%#v, %v), want first pending at attempt 1", steps, err)
	}

	const reason = "cancelled by test"
	cancelled, err := ms.CancelWorkflowRun(ctx, run.ID, reason)
	if err != nil || cancelled.Status != state.WorkflowRunStatusFailed || cancelled.LastError == nil || *cancelled.LastError != reason || cancelled.FinishedAt == nil {
		t.Fatalf("CancelWorkflowRun = (%#v, %v), want terminal failure with reason", cancelled, err)
	}
	steps, err = ms.GetWorkflowSteps(ctx, run.ID)
	if err != nil || len(steps) != 2 || steps[0].Status != state.WorkflowStepStatusSkipped || steps[1].Status != state.WorkflowStepStatusSkipped {
		t.Fatalf("cancelled steps = (%#v, %v), want both skipped", steps, err)
	}
	unchanged, err := ms.CancelWorkflowRun(ctx, run.ID, "replacement reason")
	if err != nil || unchanged.LastError == nil || *unchanged.LastError != reason {
		t.Fatalf("terminal cancellation = (%#v, %v), want original terminal result", unchanged, err)
	}
}

func TestMemStore_WorkflowRejectsInvalidRecords(t *testing.T) {
	ctx := context.Background()
	ms := state.NewMemStore()

	badStatus := &state.WorkflowRun{
		AppID:              "app-invalid",
		WorkflowName:       "bad-status",
		Status:             "not-a-status",
		DefinitionSnapshot: json.RawMessage(`{}`),
	}
	if err := ms.CreateWorkflowRun(ctx, badStatus); !errors.Is(err, state.ErrWorkflowInvalidStatus) {
		t.Fatalf("invalid run status error = %v, want ErrWorkflowInvalidStatus", err)
	}

	badDefinition := &state.WorkflowRun{
		AppID:              "app-invalid",
		WorkflowName:       "bad-definition",
		DefinitionSnapshot: json.RawMessage(`{`),
	}
	if err := ms.CreateWorkflowRun(ctx, badDefinition); !errors.Is(err, state.ErrWorkflowInvalidInput) {
		t.Fatalf("invalid definition error = %v, want ErrWorkflowInvalidInput", err)
	}

	run := &state.WorkflowRun{
		AppID:              "app-invalid",
		WorkflowName:       "valid",
		DefinitionSnapshot: json.RawMessage(`{}`),
	}
	if err := ms.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}

	if _, _, err := ms.ListWorkflowRuns(ctx, run.AppID, state.ListWorkflowRunsOpts{Offset: -1}); !errors.Is(err, state.ErrWorkflowInvalidPagination) {
		t.Fatalf("negative offset error = %v, want ErrWorkflowInvalidPagination", err)
	}
	if err := ms.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{
		StepName: "bad", Status: "not-a-status",
	}}); !errors.Is(err, state.ErrWorkflowInvalidStatus) {
		t.Fatalf("invalid step status error = %v, want ErrWorkflowInvalidStatus", err)
	}
	if err := ms.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{
		StepName: "bad-attempt", Attempt: -1,
	}}); !errors.Is(err, state.ErrWorkflowInvalidAttempt) {
		t.Fatalf("negative attempt error = %v, want ErrWorkflowInvalidAttempt", err)
	}
	if err := ms.InsertWorkflowEvent(ctx, &state.WorkflowEvent{
		RunID: run.ID, EventName: "bad-payload", Payload: json.RawMessage(`{`),
	}); !errors.Is(err, state.ErrWorkflowInvalidInput) {
		t.Fatalf("invalid event payload error = %v, want ErrWorkflowInvalidInput", err)
	}
	if err := ms.InsertWorkflowEvent(ctx, &state.WorkflowEvent{
		RunID: "missing", EventName: "event", Payload: json.RawMessage(`{}`),
	}); !errors.Is(err, state.ErrWorkflowRunNotFound) {
		t.Fatalf("missing event run error = %v, want ErrWorkflowRunNotFound", err)
	}
	if _, err := ms.SweepExpiredWorkflowRuns(ctx, -time.Second); !errors.Is(err, state.ErrWorkflowInvalidRecord) {
		t.Fatalf("negative run retention error = %v, want ErrWorkflowInvalidRecord", err)
	}
	if _, err := ms.SweepExpiredWorkflowEvents(ctx, -time.Second); !errors.Is(err, state.ErrWorkflowInvalidRecord) {
		t.Fatalf("negative event retention error = %v, want ErrWorkflowInvalidRecord", err)
	}
}

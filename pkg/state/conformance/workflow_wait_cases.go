package conformance

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func testWorkflowWaitsAndAttempts(t *testing.T, fx *Fixture) {
	t.Run("attempt_results_and_retry_deadlines", func(t *testing.T) {
		run := newWorkflowWaitConformanceRun(t, fx, "attempt-history", "success", "retry-http", "retry")
		input := json.RawMessage(`{"order_id":"ord-1"}`)
		storedInput, err := fx.Store.StartWorkflowStep(fx.Ctx, run.ID, "success", 1, input)
		if err != nil || !workflowJSONEqual(storedInput, input) {
			t.Fatalf("StartWorkflowStep = %s, %v; want stored input %s", storedInput, err, input)
		}
		okStatus := 200
		if err := fx.Store.MarkWorkflowStepAttemptStatus(fx.Ctx, run.ID, "success", state.WorkflowStepStatusSucceeded, 1, &okStatus, json.RawMessage(`{"ok":true}`), nil); err != nil {
			t.Fatalf("MarkWorkflowStepAttemptStatus(success): %v", err)
		}
		attempts, err := fx.Store.GetWorkflowStepAttempts(fx.Ctx, run.ID, "success")
		if err != nil || len(attempts) != 1 || attempts[0].Status != state.WorkflowAttemptStatusSucceeded || attempts[0].HTTPStatus == nil || *attempts[0].HTTPStatus != okStatus {
			t.Fatalf("GetWorkflowStepAttempts(success) = %+v, %v", attempts, err)
		}

		if _, err := fx.Store.StartWorkflowStep(fx.Ctx, run.ID, "retry-http", 1, input); err != nil {
			t.Fatalf("StartWorkflowStep(retry-http): %v", err)
		}
		retryAt := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
		unavailable := 503
		if err := fx.Store.ScheduleWorkflowStepRetryWithHTTPStatus(fx.Ctx, run.ID, "retry-http", 1, retryAt, &unavailable, "upstream unavailable"); err != nil {
			t.Fatalf("ScheduleWorkflowStepRetryWithHTTPStatus: %v", err)
		}
		attempts, err = fx.Store.GetWorkflowStepAttempts(fx.Ctx, run.ID, "retry-http")
		if err != nil || len(attempts) != 1 || attempts[0].Status != state.WorkflowAttemptStatusRetrying || attempts[0].HTTPStatus == nil || *attempts[0].HTTPStatus != unavailable || attempts[0].NextAttemptAt == nil || !attempts[0].NextAttemptAt.Equal(retryAt) {
			t.Fatalf("GetWorkflowStepAttempts(retry-http) = %+v, %v", attempts, err)
		}

		if _, err := fx.Store.StartWorkflowStep(fx.Ctx, run.ID, "retry", 1, input); err != nil {
			t.Fatalf("StartWorkflowStep(retry): %v", err)
		}
		plainRetryAt := retryAt.Add(time.Minute)
		if err := fx.Store.ScheduleWorkflowStepRetry(fx.Ctx, run.ID, "retry", 1, plainRetryAt, "retry requested"); err != nil {
			t.Fatalf("ScheduleWorkflowStepRetry: %v", err)
		}
		steps, err := fx.Store.GetWorkflowSteps(fx.Ctx, run.ID)
		if err != nil {
			t.Fatalf("GetWorkflowSteps(retries): %v", err)
		}
		for _, step := range steps {
			if step.StepName == "retry" && (step.NextRetryAt == nil || !step.NextRetryAt.Equal(plainRetryAt)) {
				t.Fatalf("retry step deadline = %+v, want %s", step.NextRetryAt, plainRetryAt)
			}
		}
	})

	t.Run("timer_wake_and_callback_event", func(t *testing.T) {
		timerRun := newWorkflowWaitConformanceRun(t, fx, "timer-wake", "sleep")
		deadline, err := fx.Store.ParkWorkflowTimer(fx.Ctx, timerRun.ID, "sleep", time.Hour)
		if err != nil || deadline.Before(time.Now().UTC().Add(59*time.Minute)) {
			t.Fatalf("ParkWorkflowTimer = %s, %v", deadline, err)
		}
		wakeAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
		if err := fx.Store.SetWorkflowRunWake(fx.Ctx, timerRun.ID, state.WorkflowRunStatusPending, wakeAt); err != nil {
			t.Fatalf("SetWorkflowRunWake: %v", err)
		}
		storedRun, err := fx.Store.GetWorkflowRun(fx.Ctx, timerRun.ID)
		if err != nil || storedRun.Status != state.WorkflowRunStatusPending || !storedRun.ScheduledFor.Equal(wakeAt) {
			t.Fatalf("GetWorkflowRun(after SetWorkflowRunWake) = %+v, %v", storedRun, err)
		}

		callbackRun := newWorkflowWaitConformanceRun(t, fx, "callback-event", "await")
		callbackID := uuid.NewString()
		payload := json.RawMessage(`{"delivery_id":"del-1"}`)
		duplicate, err := fx.Store.CompleteWorkflowCallback(fx.Ctx, callbackRun.ID, "await", "delivery.confirmed", callbackID, time.Hour, payload)
		if err != nil || duplicate {
			t.Fatalf("CompleteWorkflowCallback(first) = (%v, %v), want (false, nil)", duplicate, err)
		}
		event, _, err := fx.Store.ParkWorkflowEvent(fx.Ctx, callbackRun.ID, "await", "delivery.confirmed", time.Hour)
		if err != nil || event == nil || event.ID != callbackID || !workflowJSONEqual(event.Payload, payload) {
			t.Fatalf("ParkWorkflowEvent(pre-delivered callback) = %+v, %v", event, err)
		}
		duplicate, err = fx.Store.CompleteWorkflowCallback(fx.Ctx, callbackRun.ID, "await", "delivery.confirmed", callbackID, time.Hour, payload)
		if err != nil || !duplicate {
			t.Fatalf("CompleteWorkflowCallback(replay) = (%v, %v), want (true, nil)", duplicate, err)
		}

		timeoutRun := newWorkflowWaitConformanceRun(t, fx, "event-timeout", "await")
		const timeout = 5 * time.Millisecond
		if _, _, err := fx.Store.ParkWorkflowEvent(fx.Ctx, timeoutRun.ID, "await", "delivery.late", timeout); err != nil {
			t.Fatalf("ParkWorkflowEvent(timeout case): %v", err)
		}
		time.Sleep(20 * time.Millisecond)
		resolved, timedOut, err := fx.Store.ResolveWorkflowEventWait(fx.Ctx, timeoutRun.ID, "await", "delivery.late", timeout, true)
		if err != nil || resolved != nil || !timedOut {
			t.Fatalf("ResolveWorkflowEventWait = (%+v, %v, %v), want (nil, true, nil)", resolved, timedOut, err)
		}
	})

	t.Run("condition_result_closes_attempt", func(t *testing.T) {
		run := newWorkflowWaitConformanceRun(t, fx, "condition-result", "await")
		update := state.WorkflowConditionUpdate{
			RunID: run.ID, StepName: "await", Interval: time.Hour, Timeout: time.Hour, MaxAttempts: 2,
		}
		ready, err := fx.Store.ResolveWorkflowCondition(fx.Ctx, update)
		if err != nil || ready.Status != state.WorkflowConditionReady {
			t.Fatalf("ResolveWorkflowCondition(ready) = %+v, %v", ready, err)
		}
		if _, err := fx.Store.StartWorkflowStep(fx.Ctx, run.ID, "await", 1, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("StartWorkflowStep(condition): %v", err)
		}
		update.Checked = true
		update.Done = true
		update.Result = json.RawMessage(`{"done":true,"delivery_id":"del-1"}`)
		result, err := fx.Store.ResolveWorkflowCondition(fx.Ctx, update)
		if err != nil || result.Status != state.WorkflowConditionSucceeded {
			t.Fatalf("ResolveWorkflowCondition(done) = %+v, %v", result, err)
		}
	})
}

// JSON-backed stores may normalize whitespace and object key ordering. Compare
// decoded values so shared conformance checks assert JSON semantics, not the
// backend's serialization format.
func workflowJSONEqual(left, right json.RawMessage) bool {
	decode := func(raw json.RawMessage) (any, bool) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, false
		}
		return value, true
	}
	leftValue, leftOK := decode(left)
	rightValue, rightOK := decode(right)
	return leftOK && rightOK && reflect.DeepEqual(leftValue, rightValue)
}

func newWorkflowWaitConformanceRun(t *testing.T, fx *Fixture, name string, stepNames ...string) *state.WorkflowRun {
	t.Helper()
	run := &state.WorkflowRun{
		AppID: fx.App.ID, WorkflowName: name,
		Input: json.RawMessage(`{}`), DefinitionSnapshot: json.RawMessage(`{"name":"conformance"}`),
	}
	if err := fx.Store.CreateWorkflowRun(fx.Ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRun(%s): %v", name, err)
	}
	steps := make([]*state.WorkflowStep, 0, len(stepNames))
	for _, stepName := range stepNames {
		steps = append(steps, &state.WorkflowStep{StepName: stepName})
	}
	if err := fx.Store.CreateWorkflowSteps(fx.Ctx, run.ID, steps); err != nil {
		t.Fatalf("CreateWorkflowSteps(%s): %v", name, err)
	}
	return run
}

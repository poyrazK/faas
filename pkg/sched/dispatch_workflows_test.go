package sched_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type mockStepExecutor struct {
	responses map[string]struct {
		code int
		body []byte
		err  error
	}
	attempts map[string]int
}

func newMockExecutor() *mockStepExecutor {
	return &mockStepExecutor{
		responses: make(map[string]struct {
			code int
			body []byte
			err  error
		}),
		attempts: make(map[string]int),
	}
}

func (m *mockStepExecutor) ExecuteStep(_ context.Context, _ string, path, _ string, _ map[string]string, _ []byte, _ time.Duration) (int, []byte, error) {
	m.attempts[path]++
	res, ok := m.responses[path]
	if !ok {
		return 200, []byte(`{"status":"ok"}`), nil
	}
	return res.code, res.body, res.err
}

func TestWorkflowOrchestrator_LinearSuccess(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	exec := newMockExecutor()
	orch := sched.NewWorkflowOrchestrator(store, exec, nil, nil, nil)

	spec := api.WorkflowSpec{
		Name: "linear_flow",
		Steps: []api.WorkflowStepSpec{
			{Name: "step_1", Run: "step1"},
			{Name: "step_2", Run: "step2", DependsOn: []string{"step_1"}},
		},
	}
	specBytes, _ := json.Marshal(spec)

	run := &state.WorkflowRun{
		AppID:              "app-1",
		WorkflowName:       spec.Name,
		Input:              json.RawMessage(`{"init":true}`),
		DefinitionSnapshot: specBytes,
	}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}

	// Dispatch tick should claim and advance both steps to succeeded
	if err := orch.DispatchTick(ctx); err != nil {
		t.Fatalf("DispatchTick: %v", err)
	}

	finalRun, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if finalRun.Status != state.WorkflowRunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", finalRun.Status)
	}

	steps, _ := store.GetWorkflowSteps(ctx, run.ID)
	for _, s := range steps {
		if s.Status != state.WorkflowStepStatusSucceeded {
			t.Fatalf("expected step %s succeeded, got %s", s.StepName, s.Status)
		}
	}
	if exec.attempts["/step1"] != 1 || exec.attempts["/step2"] != 1 {
		t.Fatalf("run targets = %#v, want /step1 and /step2", exec.attempts)
	}
}

func TestWorkflowOrchestrator_RetryAndFail(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	exec := newMockExecutor()
	exec.responses["/flaky"] = struct {
		code int
		body []byte
		err  error
	}{code: 500, body: []byte("internal error"), err: nil}

	orch := sched.NewWorkflowOrchestrator(store, exec, nil, nil, nil)

	spec := api.WorkflowSpec{
		Name: "retry_flow",
		Steps: []api.WorkflowStepSpec{
			{
				Name: "flaky_step",
				Path: "/flaky",
				Retry: &api.WorkflowRetrySpec{
					MaxAttempts: 2,
				},
			},
		},
	}
	specBytes, _ := json.Marshal(spec)

	run := &state.WorkflowRun{
		AppID:              "app-2",
		WorkflowName:       spec.Name,
		DefinitionSnapshot: specBytes,
	}
	_ = store.CreateWorkflowRun(ctx, run)

	// Tick 1: claims run, attempts step -> 500 -> retry eligible (kept pending)
	_ = orch.DispatchTick(ctx)
	steps, _ := store.GetWorkflowSteps(ctx, run.ID)
	if steps[0].Attempt != 1 || steps[0].Status != state.WorkflowStepStatusPending {
		t.Fatalf("expected attempt 1 pending, got attempt=%d status=%s", steps[0].Attempt, steps[0].Status)
	}
	queued, _ := store.GetWorkflowRun(ctx, run.ID)
	if queued.Status != state.WorkflowRunStatusPending || !queued.ScheduledFor.After(time.Now().UTC()) {
		t.Fatalf("retry run = status %s scheduled_for %s, want pending in the future", queued.Status, queued.ScheduledFor)
	}

	// Tick 2: advances run again -> attempt 2 -> retries exhausted -> dead
	_ = orch.AdvanceWorkflowRun(ctx, run.ID)
	steps, _ = store.GetWorkflowSteps(ctx, run.ID)
	if steps[0].Attempt != 2 || steps[0].Status != state.WorkflowStepStatusDead {
		t.Fatalf("expected attempt 2 dead, got attempt=%d status=%s", steps[0].Attempt, steps[0].Status)
	}

	finalRun, _ := store.GetWorkflowRun(ctx, run.ID)
	if finalRun.Status != state.WorkflowRunStatusDead {
		t.Fatalf("expected run dead, got %s", finalRun.Status)
	}
}

func TestWorkflowOrchestrator_WaitForEvent(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	exec := newMockExecutor()
	orch := sched.NewWorkflowOrchestrator(store, exec, nil, nil, nil)

	spec := api.WorkflowSpec{
		Name: "approval_flow",
		Steps: []api.WorkflowStepSpec{
			{Name: "prepare", Path: "/prep"},
			{Name: "await_approval", WaitForEvent: "order.approved", Timeout: 1 * time.Hour, OnTimeout: "fallback", DependsOn: []string{"prepare"}},
			{Name: "fulfill", Path: "/fulfill", DependsOn: []string{"await_approval"}},
			{Name: "fallback", Path: "/fallback"},
		},
	}
	specBytes, _ := json.Marshal(spec)

	run := &state.WorkflowRun{
		AppID:              "app-3",
		WorkflowName:       spec.Name,
		DefinitionSnapshot: specBytes,
	}
	_ = store.CreateWorkflowRun(ctx, run)

	// First tick: step 1 succeeds, step 2 parks in awaiting_event
	_ = orch.DispatchTick(ctx)
	currRun, _ := store.GetWorkflowRun(ctx, run.ID)
	if currRun.Status != state.WorkflowRunStatusAwaitingEvent {
		t.Fatalf("expected awaiting_event, got %s", currRun.Status)
	}

	// External event arrives!
	eventPayload := json.RawMessage(`{"approved_by":"manager"}`)
	if err := orch.ProcessEvent(ctx, run.ID, "order.approved", eventPayload); err != nil {
		t.Fatalf("ProcessEvent: %v", err)
	}

	// Fulfill step should now have executed and the run should succeed
	finalRun, _ := store.GetWorkflowRun(ctx, run.ID)
	if finalRun.Status != state.WorkflowRunStatusSucceeded {
		t.Fatalf("expected succeeded after event, got %s", finalRun.Status)
	}
	if exec.attempts["/fallback"] != 0 {
		t.Fatalf("timeout handler attempts = %d, want 0 on event path", exec.attempts["/fallback"])
	}
	steps, _ := store.GetWorkflowSteps(ctx, run.ID)
	for _, step := range steps {
		if step.StepName == "fallback" && step.Status != state.WorkflowStepStatusSkipped {
			t.Fatalf("timeout handler status = %s, want skipped on event path", step.Status)
		}
	}
}

func TestWorkflowOrchestrator_CallbackWaitResumesWithoutPolling(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	exec := newMockExecutor()
	orch := sched.NewWorkflowOrchestrator(store, exec, nil, nil, nil)
	spec := api.WorkflowSpec{Name: "approval", Steps: []api.WorkflowStepSpec{
		{Name: "request", Run: "request_approval"},
		{Name: "await", WaitForCallback: true, Timeout: time.Hour, DependsOn: []string{"request"}},
		{Name: "fulfill", Run: "fulfill", DependsOn: []string{"await"}},
	}}
	snapshot, _ := json.Marshal(spec)
	run := &state.WorkflowRun{AppID: "app-callback", WorkflowName: spec.Name, DefinitionSnapshot: snapshot}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := orch.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	parked, _ := store.GetWorkflowRun(ctx, run.ID)
	if parked.Status != state.WorkflowRunStatusAwaitingEvent || exec.attempts["/fulfill"] != 0 {
		t.Fatalf("callback did not park: run=%s attempts=%#v", parked.Status, exec.attempts)
	}
	id := api.WorkflowCallbackID(run.ID, "await")
	payload := json.RawMessage("{\"approved\":true}")
	if duplicate, err := store.CompleteWorkflowCallback(ctx, run.ID, "await", api.WorkflowCallbackEventName(run.ID, "await"), id, time.Hour, payload); err != nil || duplicate {
		t.Fatalf("complete callback: duplicate=%t err=%v", duplicate, err)
	}
	if err := orch.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	final, _ := store.GetWorkflowRun(ctx, run.ID)
	if final.Status != state.WorkflowRunStatusSucceeded || exec.attempts["/fulfill"] != 1 {
		t.Fatalf("callback did not resume: run=%s attempts=%#v", final.Status, exec.attempts)
	}
}

func TestWorkflowOrchestrator_DurableTimerSurvivesWakeAndRestart(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	exec := newMockExecutor()
	first := sched.NewWorkflowOrchestrator(store, exec, nil, nil, nil)
	spec := api.WorkflowSpec{Name: "order", Steps: []api.WorkflowStepSpec{
		{Name: "charge", Run: "charge"},
		{Name: "wait_three_days", WaitForDuration: 500 * time.Millisecond, DependsOn: []string{"charge"}},
		{Name: "check_delivery", Run: "check_delivery", DependsOn: []string{"wait_three_days"}},
	}}
	snapshot, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: "app-order", WorkflowName: spec.Name, DefinitionSnapshot: snapshot}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := first.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	parked, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if parked.Status != state.WorkflowRunStatusAwaitingEvent || !parked.ScheduledFor.After(time.Now()) {
		t.Fatalf("parked run = %#v, want a future deadline", parked)
	}
	steps, err := store.GetWorkflowSteps(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var startedAt time.Time
	for _, step := range steps {
		if step.StepName == "wait_three_days" {
			if step.Status != state.WorkflowStepStatusAwaitingEvent || step.StartedAt == nil {
				t.Fatalf("timer step = %#v, want awaiting with activation", step)
			}
			startedAt = *step.StartedAt
		}
	}
	if startedAt.IsZero() || exec.attempts["/check_delivery"] != 0 {
		t.Fatalf("timer started = %v, delivery attempts = %d", startedAt, exec.attempts["/check_delivery"])
	}
	if !parked.ScheduledFor.Equal(startedAt.Add(spec.Steps[1].WaitForDuration)) {
		t.Fatalf("deadline = %v, want activation + wait = %v", parked.ScheduledFor, startedAt.Add(spec.Steps[1].WaitForDuration))
	}

	// An unrelated callback must not complete or shorten a timer.
	if err := first.ProcessEvent(ctx, run.ID, "unrelated.webhook", json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	reparked, _ := store.GetWorkflowRun(ctx, run.ID)
	if reparked.Status != state.WorkflowRunStatusAwaitingEvent || !reparked.ScheduledFor.Equal(parked.ScheduledFor) {
		t.Fatalf("event moved timer deadline: before=%v after=%v", parked.ScheduledFor, reparked.ScheduledFor)
	}
	if exec.attempts["/check_delivery"] != 0 {
		t.Fatal("downstream step ran before timer elapsed")
	}

	// A different orchestrator instance represents a scheduler restart.
	second := sched.NewWorkflowOrchestrator(store, exec, nil, nil, nil)
	if delay := time.Until(reparked.ScheduledFor); delay > 0 {
		time.Sleep(delay + 10*time.Millisecond)
	}
	if err := second.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	final, _ := store.GetWorkflowRun(ctx, run.ID)
	if final.Status != state.WorkflowRunStatusSucceeded {
		t.Fatalf("run status = %s, want succeeded", final.Status)
	}
	if exec.attempts["/charge"] != 1 || exec.attempts["/check_delivery"] != 1 {
		t.Fatalf("step attempts = %#v, want one call each", exec.attempts)
	}
}

func TestWorkflowOrchestrator_ParallelTimersUseEarliestDeadline(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	exec := newMockExecutor()
	orch := sched.NewWorkflowOrchestrator(store, exec, nil, nil, nil)
	spec := api.WorkflowSpec{Name: "parallel", Steps: []api.WorkflowStepSpec{
		{Name: "short", WaitForDuration: 500 * time.Millisecond},
		{Name: "long", WaitForDuration: 1500 * time.Millisecond},
		{Name: "after_short", Run: "after_short", DependsOn: []string{"short"}},
		{Name: "after_long", Run: "after_long", DependsOn: []string{"long"}},
	}}
	snapshot, _ := json.Marshal(spec)
	run := &state.WorkflowRun{AppID: "app-parallel", WorkflowName: spec.Name, DefinitionSnapshot: snapshot}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := orch.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	firstDue, _ := store.GetWorkflowRun(ctx, run.ID)
	if delay := time.Until(firstDue.ScheduledFor); delay > 0 {
		time.Sleep(delay + 10*time.Millisecond)
	}
	if err := orch.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	middle, _ := store.GetWorkflowRun(ctx, run.ID)
	if middle.Status != state.WorkflowRunStatusAwaitingEvent || exec.attempts["/after_short"] != 1 || exec.attempts["/after_long"] != 0 {
		t.Fatalf("middle = %s, attempts = %#v", middle.Status, exec.attempts)
	}
	if delay := time.Until(middle.ScheduledFor); delay > 0 {
		time.Sleep(delay + 10*time.Millisecond)
	}
	if err := orch.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	final, _ := store.GetWorkflowRun(ctx, run.ID)
	if final.Status != state.WorkflowRunStatusSucceeded || exec.attempts["/after_long"] != 1 {
		t.Fatalf("final = %s, attempts = %#v", final.Status, exec.attempts)
	}
}

func TestWorkflowOrchestrator_WaitTimeoutRunsHandler(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	exec := newMockExecutor()
	orch := sched.NewWorkflowOrchestrator(store, exec, nil, nil, nil)
	spec := api.WorkflowSpec{
		Name: "timeout_flow",
		Steps: []api.WorkflowStepSpec{
			{Name: "wait", WaitForEvent: "never", Timeout: 10 * time.Millisecond, OnTimeout: "fallback"},
			{Name: "fallback", Path: "/fallback"},
		},
	}
	specBytes, _ := json.Marshal(spec)
	run := &state.WorkflowRun{AppID: "app-timeout", WorkflowName: spec.Name, DefinitionSnapshot: specBytes}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	if err := orch.DispatchTick(ctx); err != nil {
		t.Fatalf("DispatchTick: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	if err := orch.DispatchTick(ctx); err != nil {
		t.Fatalf("timeout DispatchTick: %v", err)
	}
	finalRun, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if finalRun.Status != state.WorkflowRunStatusSucceeded {
		t.Fatalf("status = %s, want succeeded after timeout handler", finalRun.Status)
	}
	steps, _ := store.GetWorkflowSteps(ctx, run.ID)
	for _, step := range steps {
		if step.Status != state.WorkflowStepStatusSucceeded {
			t.Fatalf("step %s status = %s, want succeeded", step.StepName, step.Status)
		}
	}
}

func TestWorkflowOrchestrator_Cancel(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	exec := newMockExecutor()
	orch := sched.NewWorkflowOrchestrator(store, exec, nil, nil, nil)

	spec := api.WorkflowSpec{
		Name: "cancel_flow",
		Steps: []api.WorkflowStepSpec{
			{Name: "await", WaitForEvent: "some.event", Timeout: time.Hour},
		},
	}
	specBytes, _ := json.Marshal(spec)

	run := &state.WorkflowRun{
		AppID:              "app-4",
		WorkflowName:       spec.Name,
		DefinitionSnapshot: specBytes,
	}
	_ = store.CreateWorkflowRun(ctx, run)
	_ = orch.DispatchTick(ctx)

	// Cancel
	if err := orch.CancelRun(ctx, run.ID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	finalRun, _ := store.GetWorkflowRun(ctx, run.ID)
	if finalRun.Status != state.WorkflowRunStatusFailed {
		t.Fatalf("expected cancelled run to have status failed, got %s", finalRun.Status)
	}
}

func TestWorkflowRetention_Sweep(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	retention := sched.NewWorkflowRetention(store, nil)

	// Run finished 31 days ago (older than 30d window)
	oldRun := &state.WorkflowRun{
		AppID:              "app-retention",
		WorkflowName:       "old",
		Status:             state.WorkflowRunStatusSucceeded,
		DefinitionSnapshot: []byte(`{}`),
	}
	_ = store.CreateWorkflowRun(ctx, oldRun)
	finishedAt := time.Now().UTC().Add(-31 * 24 * time.Hour)
	_ = store.MarkWorkflowRunStatus(ctx, oldRun.ID, state.WorkflowRunStatusSucceeded, nil, nil)
	// Mutate finishedAt in memory store
	oldRunRef, _ := store.GetWorkflowRun(ctx, oldRun.ID)
	oldRunRef.FinishedAt = &finishedAt
	_ = store.MarkWorkflowRunStatus(ctx, oldRun.ID, state.WorkflowRunStatusSucceeded, nil, nil)

	// Sweep
	if err := retention.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
}

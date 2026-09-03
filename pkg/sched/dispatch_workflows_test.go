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
			{Name: "step_1", Path: "/step1"},
			{Name: "step_2", Path: "/step2", DependsOn: []string{"step_1"}},
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
			{Name: "await_approval", WaitForEvent: "order.approved", Timeout: 1 * time.Hour, DependsOn: []string{"prepare"}},
			{Name: "fulfill", Path: "/fulfill", DependsOn: []string{"await_approval"}},
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


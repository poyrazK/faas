package sched_test

// adr: 588

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type manualRetryAfterResumeExecutor struct{ calls int }

func (e *manualRetryAfterResumeExecutor) ExecuteStep(context.Context, string, string, string, map[string]string, []byte, time.Duration) (int, []byte, error) {
	e.calls++
	if e.calls < 3 {
		return 503, []byte(`{}`), nil
	}
	return 200, []byte(`{"ok":true}`), nil
}

func TestManualStepRetryDispatchesAfterResumeExhaustion(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		spec := api.WorkflowSpec{Name: "manual-retry", Steps: []api.WorkflowStepSpec{{Name: "send", Run: "send", Retry: &api.WorkflowRetrySpec{MaxAttempts: 1}}}}
		run := sched.CreateGuardedRun(t, store, spec, `{}`)
		app := makeResumeTargetLive(t, store, run)
		executor := &manualRetryAfterResumeExecutor{}
		orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := store.(state.WorkflowResumeStore).ResumeWorkflowRun(ctx, state.WorkflowResumeOptions{RunID: run.ID, AppID: app.ID, AccountID: app.AccountID}); err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.(state.WorkflowRetryStore).RetryWorkflowStep(ctx, run.ID, "send", api.PlanHobby.WorkflowMaxConcurrentRuns(), api.PlanHobby.WorkflowMaxParkedRuns()); err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		final, err := store.GetWorkflowRun(ctx, run.ID)
		if err != nil || final.Status != state.WorkflowRunStatusSucceeded || executor.calls != 3 {
			t.Fatalf("manual retry: run=%+v calls=%d err=%v", final, executor.calls, err)
		}
		attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, "send")
		if err != nil || len(attempts) != 3 || attempts[2].Attempt != 3 {
			t.Fatalf("attempt history: %+v, %v", attempts, err)
		}
	})
}

func TestManualStepRetryRejectsCancelledRunWithFailedStep(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		spec := api.WorkflowSpec{Name: "cancelled-retry", Steps: []api.WorkflowStepSpec{{Name: "send", Run: "send"}}}
		run := sched.CreateGuardedRun(t, store, spec, `{}`)
		if err := store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send", Status: state.WorkflowStepStatusFailed, Input: json.RawMessage(`{}`)}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CancelWorkflowRun(ctx, run.ID, "operator requested cancellation"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.(state.WorkflowRetryStore).RetryWorkflowStep(ctx, run.ID, "send", api.PlanHobby.WorkflowMaxConcurrentRuns(), api.PlanHobby.WorkflowMaxParkedRuns()); !errors.Is(err, state.ErrWorkflowRetryNotAllowed) {
			t.Fatalf("cancelled run accepted a retry: %v", err)
		}
	})
}

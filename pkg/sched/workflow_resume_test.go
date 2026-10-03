// adr: 495
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func makeResumeTargetLive(t *testing.T, store state.Store, run *state.WorkflowRun) state.App {
	t.Helper()
	ctx := context.Background()
	app, err := store.AppByID(ctx, run.AppID)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: "sha256:test", Workflows: json.RawMessage(`[]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	return app
}
func TestWorkflowResumeContinuesFailedBatchWithoutRepeatingSuccess(t *testing.T) {
	guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		run := createGuardedRun(t, store, foreachOrchestrationSpec(), `{"items":["a","{{input.literal}}",3]}`)
		app := makeResumeTargetLive(t, store, run)
		executor := &foreachExecutor{failSecond: true}
		orchestrator := NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		failed, _ := store.GetWorkflowRun(ctx, run.ID)
		if failed.Status != state.WorkflowRunStatusFailed || len(executor.names) != 2 {
			t.Fatalf("initial failure: %+v %v", failed, executor.names)
		}
		item := api.WorkflowForEachItemName("batch", 1)
		if _, _, _, err := store.(state.WorkflowResumeStore).ResumeWorkflowRun(ctx, state.WorkflowResumeOptions{RunID: run.ID, AppID: app.ID, AccountID: app.AccountID}); err != nil {
			t.Fatal(err)
		}
		executor.failSecond = false
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		final, _ := store.GetWorkflowRun(ctx, run.ID)
		if final.Status != state.WorkflowRunStatusSucceeded || final.ResumeCount != 1 || len(executor.names) != 4 {
			t.Fatalf("continuation: %+v calls=%v", final, executor.names)
		}
		if executor.names[0] != api.WorkflowForEachItemName("batch", 0) || executor.names[1] != item || executor.names[2] != item || executor.names[3] != api.WorkflowForEachItemName("batch", 2) || executor.keys[1] != executor.keys[2] || string(executor.inputs[1]) != string(executor.inputs[2]) {
			t.Fatalf("items replayed or payload/key changed: names=%v inputs=%q", executor.names, executor.inputs)
		}
		if !workflowJSONEqual(final.Output, []byte(`[{"index":0,"item":"a"},{"index":1,"item":"{{input.literal}}"},{"index":2,"item":3}]`)) {
			t.Fatalf("batch output=%s", final.Output)
		}
		attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, item)
		if err != nil || len(attempts) != 2 || attempts[0].Status != state.WorkflowAttemptStatusFailed || attempts[1].Status != state.WorkflowAttemptStatusSucceeded {
			t.Fatalf("history: %+v %v", attempts, err)
		}
	})
}

type resumeRetryExecutor struct {
	calls  int
	keys   []string
	inputs [][]byte
}

func (e *resumeRetryExecutor) ExecuteStep(_ context.Context, _, _, _ string, headers map[string]string, input []byte, _ time.Duration) (int, []byte, error) {
	e.calls++
	e.keys = append(e.keys, headers["Idempotency-Key"])
	e.inputs = append(e.inputs, append([]byte(nil), input...))
	if e.calls < 4 {
		return 503, []byte(`{}`), nil
	}
	return 200, []byte(`{"ok":true}`), nil
}
func TestWorkflowResumeGrantsFreshRetryBudgetAndKeepsAttemptNumbers(t *testing.T) {
	guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		spec := api.WorkflowSpec{Name: "retry", Steps: []api.WorkflowStepSpec{{Name: "send", Run: "send", Retry: &api.WorkflowRetrySpec{MaxAttempts: 2, Backoff: "exponential"}, Input: json.RawMessage(`{"literal":"{{input}}"}`)}}}
		run := createGuardedRun(t, store, spec, `{"text":"{{input.secret}}"}`)
		app := makeResumeTargetLive(t, store, run)
		executor := &resumeRetryExecutor{}
		orchestrator := NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		tickUntil := func(want string) {
			t.Helper()
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				if err := orchestrator.DispatchTick(ctx); err != nil {
					t.Fatal(err)
				}
				current, _ := store.GetWorkflowRun(ctx, run.ID)
				if current.Status == want {
					return
				}
				time.Sleep(25 * time.Millisecond)
			}
			t.Fatal("run never reached", want)
		}
		tickUntil(state.WorkflowRunStatusDead)
		if _, _, _, err := store.(state.WorkflowResumeStore).ResumeWorkflowRun(ctx, state.WorkflowResumeOptions{RunID: run.ID, AppID: app.ID, AccountID: app.AccountID}); err != nil {
			t.Fatal(err)
		}
		tickUntil(state.WorkflowRunStatusSucceeded)
		if executor.calls != 4 {
			t.Fatalf("fresh retry budget lost: %d calls", executor.calls)
		}
		for i := 1; i < len(executor.keys); i++ {
			if executor.keys[i] != executor.keys[0] || string(executor.inputs[i]) != string(executor.inputs[0]) {
				t.Fatal("retry input or logical action identity changed")
			}
		}
		attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, "send")
		if err != nil || len(attempts) != 4 {
			t.Fatalf("attempts=%+v %v", attempts, err)
		}
		for i, a := range attempts {
			if a.Attempt != i+1 {
				t.Fatal("attempt history overwritten")
			}
		}
		old := state.WithWorkflowRunGeneration(ctx, run.ID, 0)
		if err := orchestrator.AdvanceWorkflowRun(old, run.ID); !errors.Is(err, state.ErrWorkflowOutboundAttemptExpired) {
			t.Fatalf("old scheduler followed new generation: %v", err)
		}
	})
}

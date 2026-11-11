// adr: 20261111163000
package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workflowcode"
)

type workflowRecoveryExecutor struct {
	paths []string
	code  bool
}

func (e *workflowRecoveryExecutor) ExecuteStep(_ context.Context, _, path, _ string, _ map[string]string, _ []byte, _ time.Duration) (int, []byte, error) {
	e.paths = append(e.paths, path)
	if e.code {
		raw, _ := json.Marshal(workflowcode.Response{Protocol: api.WorkflowCodeProtocol, Result: workflowcode.Result{Status: workflowcode.ResultSucceeded, Output: json.RawMessage(`{}`)}})
		return 200, raw, nil
	}
	return 200, []byte(`{}`), nil
}

func TestWorkflowRejectsTimeoutRouteCycles(t *testing.T) {
	cases := map[string]api.WorkflowSpec{
		"self": {Name: "cycle", Steps: []api.WorkflowStepSpec{{Name: "wait", WaitForEvent: "ready", Timeout: time.Second, OnTimeout: "wait"}}},
		"dependency": {Name: "cycle", Steps: []api.WorkflowStepSpec{
			{Name: "wait", WaitForEvent: "ready", Timeout: time.Second, OnTimeout: "fallback", DependsOn: []string{"fallback"}},
			{Name: "fallback", Run: "notify"},
		}},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := api.ValidateWorkflowDAG(spec, api.PlanHobby); err != nil {
				return
			}
			store := state.NewMemStore()
			run := sched.CreateGuardedRun(t, store, spec, `{}`)
			e := &workflowRecoveryExecutor{}
			if err := sched.NewWorkflowOrchestrator(store, e, nil, nil, nil).DispatchTick(context.Background()); err != nil {
				t.Fatal(err)
			}
			current, _ := store.GetWorkflowRun(context.Background(), run.ID)
			steps, _ := store.GetWorkflowSteps(context.Background(), run.ID)
			for _, step := range steps {
				t.Logf("step=%s status=%s started=%v", step.StepName, step.Status, step.StartedAt)
			}
			t.Fatalf("validator accepted timeout cycle: run=%s executor_calls=%d; no timer/event wait started", current.Status, len(e.paths))
		})
	}
}

func TestWorkflowRejectsExternalTimeoutMarker(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		spec := api.WorkflowSpec{Name: "payload", Steps: []api.WorkflowStepSpec{
			{Name: "wait", WaitForEvent: "ready", Timeout: time.Hour, OnTimeout: "fallback"},
			{Name: "fallback", Run: "notify"},
		}}
		if _, err := api.ValidateWorkflowDAG(spec, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		run := sched.CreateGuardedRun(t, store, spec, `{}`)
		if err := store.InsertWorkflowEvent(context.Background(), &state.WorkflowEvent{RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`{"timeout":true}`)}); !errors.Is(err, state.ErrWorkflowInvalidInput) {
			t.Fatalf("reserved timeout marker was not rejected: %v", err)
		}
		e := &workflowRecoveryExecutor{}
		if err := sched.NewWorkflowOrchestrator(store, e, nil, nil, nil).DispatchTick(context.Background()); err != nil {
			t.Fatal(err)
		}
		current, _ := store.GetWorkflowRun(context.Background(), run.ID)
		if len(e.paths) != 0 {
			t.Fatalf("received event incorrectly ran timeout fallback: paths=%v status=%s", e.paths, current.Status)
		}
	})
}

func TestWorkflowValidHTMLInputReachesCodeHandler(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		for _, char := range []string{"x", "<"} {
			t.Run(char, func(t *testing.T) {
				input := `"` + strings.Repeat(char, 600*1024) + `"`
				if int64(len(input)) >= api.WorkflowRunInputMaxBytes {
					t.Fatal("fixture exceeds input limit")
				}
				spec := api.WorkflowSpec{Name: "html", Code: &api.WorkflowCodeSpec{Run: "html"}}
				run := sched.CreateGuardedRun(t, store, spec, input)
				e := &workflowRecoveryExecutor{code: true}
				if err := sched.NewWorkflowOrchestrator(store, e, nil, nil, nil).DispatchTick(context.Background()); err != nil {
					t.Fatal(err)
				}
				current, _ := store.GetWorkflowRun(context.Background(), run.ID)
				message := ""
				if current.LastError != nil {
					message = *current.LastError
				}
				if len(e.paths) != 1 || current.Status != state.WorkflowRunStatusSucceeded {
					t.Fatalf("valid input failed before handler: input_bytes=%d limit=%d calls=%d status=%s error=%s", len(input), api.WorkflowRunInputMaxBytes, len(e.paths), current.Status, message)
				}
			})
		}
	})
}

func TestWorkflowExpiredWorkerCannotRewriteRecoveredResult(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := context.Background()
	spec := api.WorkflowSpec{Name: "lease", Steps: []api.WorkflowStepSpec{{Name: "send", Run: "send"}}}
	run := sched.CreateGuardedRun(t, store, spec, `{}`)
	first, err := store.ClaimNextDueWorkflowRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldCtx := state.WithWorkflowRunGeneration(ctx, run.ID, first.ResumeCount)
	if err := store.CreateWorkflowSteps(oldCtx, run.ID, []*state.WorkflowStep{{RunID: run.ID, StepName: "send", Status: state.WorkflowStepStatusPending, Input: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartWorkflowStep(oldCtx, run.ID, "send", 1, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	// Simulate the first worker being paused beyond its timeout plus lease
	// grace. Only this test's isolated database is modified.
	if _, err := pool.Exec(ctx, `UPDATE workflow_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimNextDueWorkflowRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newCtx := state.WithWorkflowRunGeneration(ctx, run.ID, second.ResumeCount)
	steps, err := store.GetWorkflowSteps(newCtx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempt := steps[0].Attempt + 1
	if _, err := store.StartWorkflowStep(newCtx, run.ID, "send", attempt, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	httpStatus := 200
	if err := store.MarkWorkflowStepAttemptStatus(newCtx, run.ID, "send", state.WorkflowStepStatusSucceeded, attempt, &httpStatus, json.RawMessage(`{"worker":"current"}`), nil); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(newCtx, run.ID, state.WorkflowRunStatusSucceeded, json.RawMessage(`{"worker":"current"}`), nil); err != nil {
		t.Fatal(err)
	}
	err = store.MarkWorkflowStepAttemptStatus(oldCtx, run.ID, "send", state.WorkflowStepStatusSucceeded, 1, &httpStatus, json.RawMessage(`{"worker":"expired"}`), nil)
	if err == nil {
		steps, _ = store.GetWorkflowSteps(ctx, run.ID)
		final, _ := store.GetWorkflowRun(ctx, run.ID)
		t.Fatalf("expired worker overwrote recovered terminal step: old_generation=%d new_generation=%d new_attempt=%d step_output=%s run_output=%s", first.ResumeCount, second.ResumeCount, attempt, steps[0].Output, final.Output)
	}
}

func TestWorkflowHTTPAttemptsCannotBeReused(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := t.Context()
		run := sched.CreateGuardedRun(t, store, api.WorkflowSpec{Name: "claim-fence", Steps: []api.WorkflowStepSpec{{Name: "send", Run: "send"}}}, `{}`)
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		// Recovery has preserved attempt 1. An old worker still holds a
		// pre-start snapshot at attempt 0 and tries to start attempt 1 again.
		if err := store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send", Status: state.WorkflowStepStatusPending, Attempt: 1}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 1, json.RawMessage(`{}`)); err == nil {
			t.Fatal("a stale pre-start snapshot reused a preserved attempt number")
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 2, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("fresh next attempt rejected: %v", err)
		}
	})
}

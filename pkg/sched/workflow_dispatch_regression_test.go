// adr: 20261111163000
package sched_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workflowcode"
)

type workflowRegressionExecutor struct {
	calls    atomic.Int32
	response []byte
}

func (e *workflowRegressionExecutor) ExecuteStep(_ context.Context, _, _, _ string, _ map[string]string, _ []byte, _ time.Duration) (int, []byte, error) {
	e.calls.Add(1)
	if e.response != nil {
		return 200, e.response, nil
	}
	return 200, []byte(`{}`), nil
}

func TestWorkflowParkedBranchDoesNotBlockForEach(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		spec := api.WorkflowSpec{Name: "audit", Steps: []api.WorkflowStepSpec{
			{Name: "a_wait", WaitForDuration: time.Hour},
			{Name: "z_batch", ForEach: &api.WorkflowForEachSpec{Items: "input.items", MaxParallel: 2, Action: api.WorkflowForEachActionSpec{Run: "send"}}},
		}}
		if _, err := api.ValidateWorkflowDAG(spec, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		run := sched.CreateGuardedRun(t, store, spec, `{"items":[1,2]}`)
		e := &workflowRegressionExecutor{}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := sched.NewWorkflowOrchestrator(store, e, nil, nil, nil).DispatchTick(ctx)
		current, _ := store.GetWorkflowRun(context.Background(), run.ID)
		steps, _ := store.GetWorkflowSteps(context.Background(), run.ID)
		for _, step := range steps {
			t.Logf("step=%s status=%s attempt=%d", step.StepName, step.Status, step.Attempt)
		}
		if e.calls.Load() != 2 || err != nil {
			t.Fatalf("runnable foreach blocked by independent timer: calls=%d status=%s err=%v", e.calls.Load(), current.Status, err)
		}
	})
}

func TestWorkflowCodeStepNULSettles(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		spec := api.WorkflowSpec{Name: "audit", Code: &api.WorkflowCodeSpec{Run: "audit"}}
		run := sched.CreateGuardedRun(t, store, spec, `{}`)
		response, err := json.Marshal(workflowcode.Response{Protocol: api.WorkflowCodeProtocol,
			Ops:    []workflowcode.ReportedOp{{Seq: 0, Name: "send", Kind: workflowcode.KindStep, Status: workflowcode.ResultFailed, Error: "bad\x00provider", NonRetryable: true}},
			Result: workflowcode.Result{Status: workflowcode.ResultRunning}})
		if err != nil {
			t.Fatal(err)
		}
		e := &workflowRegressionExecutor{response: response}
		orch := sched.NewWorkflowOrchestrator(store, e, nil, nil, nil)
		if err := orch.DispatchTick(context.Background()); err != nil {
			current, _ := store.GetWorkflowRun(context.Background(), run.ID)
			t.Fatalf("code step error was not safely persisted: status=%s calls=%d err=%v", current.Status, e.calls.Load(), err)
		}
	})
}

func TestWorkflowTextHandlerDoesNotBypassRetryBudget(t *testing.T) {
	store := state.NewMemStore()
	spec := api.WorkflowSpec{Name: "audit", Steps: []api.WorkflowStepSpec{{Name: "send", Run: "send", Retry: &api.WorkflowRetrySpec{MaxAttempts: 1}}}}
	if _, err := api.ValidateWorkflowDAG(spec, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	run := sched.CreateGuardedRun(t, store, spec, `{}`)
	e := &workflowRegressionExecutor{response: []byte("OK")}
	orch := sched.NewWorkflowOrchestrator(store, e, nil, nil, nil)
	for i := 0; i < 4; i++ {
		err := orch.DispatchTick(context.Background())
		t.Logf("tick=%d calls=%d err=%v", i+1, e.calls.Load(), err)
	}
	current, _ := store.GetWorkflowRun(context.Background(), run.ID)
	steps, _ := store.GetWorkflowSteps(context.Background(), run.ID)
	if e.calls.Load() > 1 || current.Status == state.WorkflowRunStatusPending {
		t.Fatalf("200 text response bypassed max_attempts=1: calls=%d run=%s step_attempt=%d step_status=%s", e.calls.Load(), current.Status, steps[0].Attempt, steps[0].Status)
	}
}

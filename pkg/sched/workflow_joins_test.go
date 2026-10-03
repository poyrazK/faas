// adr: 436
package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type joinExecutor struct {
	paths      []string
	bodies     [][]byte
	retryCRM   bool
	failBranch bool
}

func (e *joinExecutor) ExecuteStep(_ context.Context, _, path, _ string, _ map[string]string, body []byte, _ time.Duration) (int, []byte, error) {
	e.paths = append(e.paths, path)
	if path == "/crm" {
		e.bodies = append(e.bodies, slices.Clone(body))
		if e.retryCRM && len(e.bodies) == 1 {
			return 503, []byte(`{"retry":true}`), nil
		}
	}
	if e.failBranch && path == "/a_end" {
		return 400, []byte(`{"failed":true}`), nil
	}
	return 200, []byte(fmt.Sprintf(`{"path":%q,"n":9007199254740993,"items":[true,null]}`, path)), nil
}

func joinedBranchSpec() api.WorkflowSpec {
	return api.WorkflowSpec{Name: "branches", Steps: []api.WorkflowStepSpec{
		{Name: "00-crm", Run: "crm", DependsOn: []string{"a-merge"}, Input: json.RawMessage(`{"source":"{{steps.a-merge.output.source}}","result":"{{steps.a-merge.output.value}}"}`), Retry: &api.WorkflowRetrySpec{MaxAttempts: 2}},
		{Name: "a-merge", DependsOn: []string{"m-a-end", "m-b-end"}, Join: &api.WorkflowJoinSpec{OutputFrom: []string{"m-b-end", "m-a-end"}}},
		{Name: "m-a-end", Run: "a_end", DependsOn: []string{"z-a"}, Retry: &api.WorkflowRetrySpec{MaxAttempts: 1}},
		{Name: "m-b-end", Run: "b_end", DependsOn: []string{"z-b"}},
		{Name: "z-a", Run: "a", When: &api.WorkflowGuardSpec{Ref: "input.branch", Op: "eq", Value: json.RawMessage(`"a"`)}},
		{Name: "z-b", Run: "b", When: &api.WorkflowGuardSpec{Ref: "input.branch", Op: "eq", Value: json.RawMessage(`"b"`)}},
	}}
}

func TestWorkflowJoinSharesContinuationAcrossConditionalBranches(t *testing.T) {
	for _, branch := range []string{"a", "b", "none"} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%v", branch, reverse), func(t *testing.T) {
				guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
					ctx := context.Background()
					spec := joinedBranchSpec()
					if reverse {
						slices.Reverse(spec.Steps)
					}
					if _, err := api.ValidateWorkflowDAG(spec, api.PlanHobby); err != nil {
						t.Fatal(err)
					}
					run := createGuardedRun(t, store, spec, fmt.Sprintf(`{"branch":%q}`, branch))
					executor := &joinExecutor{}
					if err := NewWorkflowOrchestrator(store, executor, nil, nil, nil).DispatchTick(ctx); err != nil {
						t.Fatal(err)
					}
					final, err := store.GetWorkflowRun(ctx, run.ID)
					if err != nil || final.Status != state.WorkflowRunStatusSucceeded {
						t.Fatalf("join stranded run: %+v %v", final, err)
					}
					if branch == "none" {
						if len(executor.paths) != 0 {
							t.Fatalf("inactive branches executed: %v", executor.paths)
						}
					} else {
						wantPaths := []string{"/" + branch, "/" + branch + "_end", "/crm"}
						wantBody := `{"source":"m-` + branch + `-end","result":{"path":"/` + branch + `_end","n":9007199254740993,"items":[true,null]}}`
						if !slices.Equal(executor.paths, wantPaths) || len(executor.bodies) != 1 || !workflowJSONEqual(executor.bodies[0], []byte(wantBody)) {
							t.Fatalf("shared continuation wrong: paths=%v bodies=%s", executor.paths, executor.bodies)
						}
					}
					steps, err := store.GetWorkflowSteps(ctx, run.ID)
					if err != nil {
						t.Fatal(err)
					}
					for _, step := range steps {
						if step.StepName == "a-merge" && (step.Attempt != 0 || step.FinishedAt == nil || (branch == "none" && step.Status != state.WorkflowStepStatusSkipped)) {
							t.Fatalf("join allocated attempt or did not close: %+v", step)
						}
					}
				})
			})
		}
	}
}

func TestWorkflowJoinContinuationRetryUsesCommittedSelection(t *testing.T) {
	guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		run := createGuardedRun(t, store, joinedBranchSpec(), `{"branch":"a"}`)
		executor := &joinExecutor{retryCRM: true}
		orchestrator := NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		if len(executor.bodies) != 1 {
			t.Fatalf("first continuation missing: %v", executor.paths)
		}
		if err := store.MarkWorkflowStepStatus(ctx, run.ID, "m-a-end", state.WorkflowStepStatusSucceeded, 1, json.RawMessage(`{"changed":true}`), nil); err != nil {
			t.Fatal(err)
		}
		steps, err := store.GetWorkflowSteps(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		var retryAt *time.Time
		for _, step := range steps {
			if step.StepName == "00-crm" {
				retryAt = step.NextRetryAt
			}
		}
		if retryAt == nil {
			t.Fatal("continuation did not persist its retry deadline")
		}
		if err := store.RecoverWorkflowRun(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Until(*retryAt) + time.Millisecond)
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		final, err := store.GetWorkflowRun(ctx, run.ID)
		if err != nil || final.Status != state.WorkflowRunStatusSucceeded || len(executor.bodies) != 2 || !workflowJSONEqual(executor.bodies[0], executor.bodies[1]) {
			t.Fatalf("retry changed selection or stranded run: %+v bodies=%s err=%v", final, executor.bodies, err)
		}
	})
}

func TestWorkflowJoinDoesNotContinueFailedBranch(t *testing.T) {
	guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		run := createGuardedRun(t, store, joinedBranchSpec(), `{"branch":"a"}`)
		executor := &joinExecutor{failBranch: true}
		if err := NewWorkflowOrchestrator(store, executor, nil, nil, nil).DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		final, err := store.GetWorkflowRun(ctx, run.ID)
		if err != nil || (final.Status != state.WorkflowRunStatusFailed && final.Status != state.WorkflowRunStatusDead) || len(executor.bodies) != 0 {
			t.Fatalf("join hid failure: %+v paths=%v err=%v", final, executor.paths, err)
		}
	})
}

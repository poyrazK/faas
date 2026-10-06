// adr: 490
package sched

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type guardExecutor struct{ paths []string }

func (e *guardExecutor) ExecuteStep(_ context.Context, _, path, _ string, _ map[string]string, _ []byte, _ time.Duration) (int, []byte, error) {
	e.paths = append(e.paths, path)
	return 200, []byte(`{"overdue":true}`), nil
}

func (e *guardExecutor) ExecuteOutboundStep(_ context.Context, _, _ string, _ int, _ api.WorkflowOutboundSpec, _ []byte, _ time.Duration) (int, []byte, time.Time, error) {
	e.paths = append(e.paths, "outbound")
	return 200, []byte(`{"status":200,"body":{"overdue":true}}`), time.Time{}, nil
}

func guardOrchestrationStores(t *testing.T, test func(*testing.T, state.Store)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { test(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { test(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}

func createGuardedRun(t *testing.T, store state.Store, spec api.WorkflowSpec, input string) *state.WorkflowRun {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "guard-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(spec)
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, Input: json.RawMessage(input), DefinitionSnapshot: snapshot}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestWorkflowGuardsSelectBranchAndFinishSkippedDescendants(t *testing.T) {
	guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		guard := api.WorkflowGuardSpec{Ref: "steps.lookup.output.overdue", Op: "eq", Value: json.RawMessage("true")}
		spec := api.WorkflowSpec{Name: "invoice", Steps: []api.WorkflowStepSpec{
			{Name: "a-child", Run: "wrong_child", DependsOn: []string{"z-receipt"}},
			{Name: "z-receipt", Run: "receipt", DependsOn: []string{"lookup"}, When: &api.WorkflowGuardSpec{Not: &guard}},
			{Name: "remind", Run: "remind", DependsOn: []string{"lookup"}, When: &guard},
			{Name: "lookup", Run: "lookup"},
		}}
		if _, err := api.ValidateWorkflowDAG(spec, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		run := createGuardedRun(t, store, spec, `{}`)
		executor := &guardExecutor{}
		if err := NewWorkflowOrchestrator(store, executor, nil, nil, nil).DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		final, _ := store.GetWorkflowRun(ctx, run.ID)
		if final.Status != state.WorkflowRunStatusSucceeded || len(executor.paths) != 2 || executor.paths[0] != "/lookup" || executor.paths[1] != "/remind" {
			t.Fatalf("wrong branch executed or run stranded: status=%s paths=%v", final.Status, executor.paths)
		}
		steps, _ := store.GetWorkflowSteps(ctx, run.ID)
		for _, step := range steps {
			if step.StepName != "a-child" && step.StepName != "z-receipt" {
				continue
			}
			if step.Status != state.WorkflowStepStatusSkipped || step.Attempt != 0 || step.SkipReason == nil {
				t.Fatalf("inactive branch not closed: %+v", step)
			}
			if step.StepName == "a-child" && (*step.SkipReason != state.WorkflowSkipDependencySkipped || step.WhenEvaluatedAt != nil) {
				t.Fatalf("descendant skip not explained: %+v", step)
			}
		}
	})
}

func TestWorkflowFalseGuardsDoNotActivateAnyTarget(t *testing.T) {
	guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		guard := &api.WorkflowGuardSpec{Ref: "input.active", Op: "eq", Value: json.RawMessage("true")}
		spec := api.WorkflowSpec{Name: "inactive", Steps: []api.WorkflowStepSpec{
			{Name: "app", Run: "send", When: guard},
			{Name: "http", Path: "/send", When: guard},
			{Name: "provider", Outbound: &api.WorkflowOutboundSpec{IntegrationID: uuid.NewString(), Method: "POST", Path: "/send"}, When: guard},
			{Name: "event", WaitForEvent: "approval", Timeout: time.Minute, When: guard},
			{Name: "callback", WaitForCallback: true, Timeout: time.Minute, When: guard},
			{Name: "timer", WaitForDuration: time.Second, When: guard},
			{Name: "poll", WaitForCondition: &api.WorkflowConditionSpec{Run: "poll", Interval: time.Minute, MaxAttempts: 2}, Timeout: time.Hour, When: guard},
		}}
		if _, err := api.ValidateWorkflowDAG(spec, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		run := createGuardedRun(t, store, spec, `{"active":false}`)
		executor := &guardExecutor{}
		if err := NewWorkflowOrchestrator(store, executor, nil, nil, nil).WithOutboundExecutor(executor).DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		final, _ := store.GetWorkflowRun(ctx, run.ID)
		if final.Status != state.WorkflowRunStatusSucceeded || len(executor.paths) != 0 {
			t.Fatalf("false guard activated work: status=%s paths=%v", final.Status, executor.paths)
		}
		steps, _ := store.GetWorkflowSteps(ctx, run.ID)
		for _, step := range steps {
			if step.Status != state.WorkflowStepStatusSkipped || step.Attempt != 0 || step.StartedAt != nil || step.NextCheckAt != nil || step.NextRetryAt != nil || step.WhenMatched == nil || *step.WhenMatched {
				t.Fatalf("false guard activated target: %+v", step)
			}
		}
	})
}

func TestWorkflowGuardRuntimeErrorFailsWithoutExecuting(t *testing.T) {
	store := state.NewMemStore()
	guard := &api.WorkflowGuardSpec{Ref: "input.amount", Op: "gt", Value: json.RawMessage("1")}
	spec := api.WorkflowSpec{Name: "invalid-data", Steps: []api.WorkflowStepSpec{{Name: "send", Run: "send", When: guard}}}
	run := createGuardedRun(t, store, spec, `{"amount":1e1000000000}`)
	executor := &guardExecutor{}
	if err := NewWorkflowOrchestrator(store, executor, nil, nil, nil).DispatchTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	final, _ := store.GetWorkflowRun(context.Background(), run.ID)
	if final.Status != state.WorkflowRunStatusDead || len(executor.paths) != 0 || final.LastError == nil || *final.LastError != state.ErrWorkflowGuardEvaluation.Error() {
		t.Fatalf("invalid guard source executed or leaked input: %+v %v", final, executor.paths)
	}
}

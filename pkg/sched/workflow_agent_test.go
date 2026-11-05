// adr: 20261027090010 — an automation's agent step admits one run, waits for its end
// without holding the scheduler, and continues with the run's result.
package sched_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// fakeAgents admits runs and reports the ends a test sets, once.
type fakeAgents struct {
	mu         sync.Mutex
	admissions []agentstore.Admission
	ends       []agentstore.WorkflowRunEnd
	err        error
}

func (f *fakeAgents) AdmitRun(_ context.Context, a agentstore.Admission) (agentstore.Run, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return agentstore.Run{}, false, f.err
	}
	f.admissions = append(f.admissions, a)
	return agentstore.Run{ID: "run-1"}, false, nil
}

func (f *fakeAgents) UndeliveredWorkflowRunEnds(context.Context, int) ([]agentstore.WorkflowRunEnd, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ends := f.ends
	f.ends = nil
	return ends, nil
}

// recordingExecutor answers every function step with its input.
type recordingExecutor struct {
	mu     sync.Mutex
	inputs [][]byte
}

func (e *recordingExecutor) ExecuteStep(_ context.Context, _, _, _ string, _ map[string]string, input []byte, _ time.Duration) (int, []byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inputs = append(e.inputs, append([]byte(nil), input...))
	return 200, []byte(`{"ok":true}`), nil
}

func agentStepRun(t *testing.T, store state.Store) *state.WorkflowRun {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "agent-step-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.WorkflowSpec{Name: "triage", Steps: []api.WorkflowStepSpec{
		{Name: "review", Agent: "pr-triage", Input: json.RawMessage(`{"inputs":{"pr":"{{input.pr}}"},"github":{"repository":"acme/app","number":"{{input.pr}}"}}`)},
		{Name: "after", Run: "after", DependsOn: []string{"review"}, Input: json.RawMessage(`{"verdict":"{{steps.review.output.result.verdict}}"}`)},
	}}
	snapshot, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, Input: json.RawMessage(`{"pr":7}`), DefinitionSnapshot: snapshot,
		ScheduledFor: time.Now().UTC().Add(-time.Second)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	return run
}

func stepStatus(t *testing.T, store state.Store, runID, name string) string {
	t.Helper()
	steps, err := store.GetWorkflowSteps(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if s.StepName == name {
			return s.Status
		}
	}
	return ""
}

func TestWorkflowAgentStepRunsAndContinues(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		run := agentStepRun(t, store)
		agents, executor := &fakeAgents{}, &recordingExecutor{}
		orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil).WithAgents(agents)
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.AdvanceWorkflowRun(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
		if len(agents.admissions) != 1 {
			t.Fatalf("admissions %d", len(agents.admissions))
		}
		a := agents.admissions[0]
		event := api.WorkflowAgentEventName(run.ID, "review", 1)
		if a.Agent != "pr-triage" || a.Trigger != "workflow" || string(a.Inputs) != `{"pr":7}` || a.GitHub == nil || a.GitHub.Number != 7 ||
			a.Workflow == nil || a.Workflow.Event != event || a.Workflow.RunID != run.ID || a.Limits != agentstore.PlanLimits(api.PlanPro) {
			t.Fatalf("admission %+v", a)
		}
		if got := stepStatus(t, store, run.ID, "review"); got != state.WorkflowStepStatusAwaitingEvent || len(executor.inputs) != 0 {
			t.Fatalf("while the run works: review %s, calls %d", got, len(executor.inputs))
		}
		agents.ends = []agentstore.WorkflowRunEnd{{Link: *a.Workflow, Output: api.WorkflowAgentStepOutput{RunID: "run-1",
			State: agentstore.StateSucceeded, Result: json.RawMessage(`{"verdict":"ship it"}`)}}}
		if err := orchestrator.DeliverAgentRunEnds(ctx); err != nil {
			t.Fatal(err)
		}
		if got := stepStatus(t, store, run.ID, "review"); got != state.WorkflowStepStatusSucceeded {
			t.Fatalf("after the run: review %s", got)
		}
		var next map[string]string
		if len(executor.inputs) != 1 || json.Unmarshal(executor.inputs[0], &next) != nil || next["verdict"] != "ship it" {
			t.Fatalf("next step input %q", executor.inputs)
		}
		got, err := store.GetWorkflowRun(ctx, run.ID)
		if err != nil || got.Status != state.WorkflowRunStatusSucceeded {
			t.Fatalf("workflow run %+v %v", got, err)
		}
	})
}

func TestWorkflowAgentStepFailsWithItsRun(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		run := agentStepRun(t, store)
		agents := &fakeAgents{}
		orchestrator := sched.NewWorkflowOrchestrator(store, &recordingExecutor{}, nil, nil, nil).WithAgents(agents)
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		if len(agents.admissions) != 1 {
			t.Fatalf("admissions %d", len(agents.admissions))
		}
		agents.ends = []agentstore.WorkflowRunEnd{{Link: *agents.admissions[0].Workflow, Output: api.WorkflowAgentStepOutput{RunID: "run-1",
			State: agentstore.StateFailed, Outcome: &api.AgentRunOutcome{Code: "delivery_failed"}}}}
		if err := orchestrator.DeliverAgentRunEnds(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetWorkflowRun(ctx, run.ID)
		if err != nil || got.Status != state.WorkflowRunStatusFailed || got.LastError == nil || *got.LastError != "agent run run-1 ended failed: delivery_failed" {
			t.Fatalf("workflow run %+v %v", got, err)
		}
	})
}

func TestWorkflowAgentStepThatCannotRun(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		for name, orchestrator := range map[string]*sched.WorkflowOrchestrator{
			"no agents":     sched.NewWorkflowOrchestrator(store, &recordingExecutor{}, nil, nil, nil),
			"unknown agent": sched.NewWorkflowOrchestrator(store, &recordingExecutor{}, nil, nil, nil).WithAgents(&fakeAgents{err: agentstore.ErrNotFound}),
		} {
			run := agentStepRun(t, store)
			if err := orchestrator.DispatchTick(ctx); err != nil {
				t.Fatal(err)
			}
			if got, err := store.GetWorkflowRun(ctx, run.ID); err != nil || got.Status != state.WorkflowRunStatusFailed {
				t.Errorf("%s: %+v %v", name, got, err)
			}
		}
	})
}

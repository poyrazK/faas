// adr: 081
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

// Shuffle the persisted creation order independently of actual completion.
// This reproduces PostgreSQL's tied created_at/step_name ordering without
// relying on wall-clock sleeps or a particular MemStore iteration order.
func TestWorkflowOutputFollowsCompletion(t *testing.T) {
	for _, tc := range []struct {
		name       string
		lastStatus string
		lastOutput json.RawMessage
		sameTime   bool
		want       string
	}{
		{"sequential", state.WorkflowStepStatusSucceeded, json.RawMessage(`{"step":"end"}`), false, `{"step":"end"}`},
		{"empty final", state.WorkflowStepStatusSucceeded, nil, false, `{"step":"start"}`},
		{"skipped branch", state.WorkflowStepStatusSkipped, json.RawMessage(`{"step":"skipped"}`), false, `{"step":"start"}`},
		{"tied sequential timestamps", state.WorkflowStepStatusSucceeded, json.RawMessage(`{"step":"end"}`), true, `{"step":"end"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			spec := api.WorkflowSpec{Name: "output", Steps: []api.WorkflowStepSpec{{Name: "start", Run: "start"}, {Name: "end", Run: "end", DependsOn: []string{"start"}}}}
			definition, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			run := &state.WorkflowRun{AppID: "output-app", WorkflowName: spec.Name, DefinitionSnapshot: definition}
			if err := store.CreateWorkflowRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			startAt := time.Now().UTC().Add(-time.Minute)
			endAt := startAt.Add(time.Second)
			if tc.sameTime {
				endAt = startAt
			}
			steps := []*state.WorkflowStep{
				{RunID: run.ID, StepName: "end", Status: tc.lastStatus, Output: tc.lastOutput, FinishedAt: &endAt},
				{RunID: run.ID, StepName: "start", Status: state.WorkflowStepStatusSucceeded, Output: json.RawMessage(`{"step":"start"}`), FinishedAt: &startAt},
			}
			if err := store.CreateWorkflowSteps(ctx, run.ID, steps); err != nil {
				t.Fatal(err)
			}
			if err := sched.NewWorkflowOrchestrator(store, newMockExecutor(), nil, nil, nil).AdvanceWorkflowRun(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			final, err := store.GetWorkflowRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if final.Status != state.WorkflowRunStatusSucceeded || string(final.Output) != tc.want {
				t.Fatalf("run status/output = %s/%s, want succeeded/%s", final.Status, final.Output, tc.want)
			}
		})
	}
}

//go:build !no_pg

// adr: 081
package sched_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowOutputPostgresUsesFinishedStep(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "workflow-output@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "workflow-output", Type: state.AppTypeApp, RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.WorkflowSpec{Name: "output", Steps: []api.WorkflowStepSpec{
		{Name: "end", Run: "end", DependsOn: []string{"start"}}, {Name: "start", Run: "start"},
	}}
	definition, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, DefinitionSnapshot: definition}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	// Both steps share created_at in the insertion transaction. PostgreSQL
	// then returns end before start, regardless of execution order.
	if err := store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "end"}, {StepName: "start"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"start", "end"} {
		output, err := json.Marshal(map[string]string{"step": name})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkWorkflowStepStatus(ctx, run.ID, name, state.WorkflowStepStatusSucceeded, 1, output, nil); err != nil {
			t.Fatal(err)
		}
	}
	steps, err := store.GetWorkflowSteps(ctx, run.ID)
	if err != nil || len(steps) != 2 || steps[0].StepName != "end" || !steps[0].CreatedAt.Equal(steps[1].CreatedAt) {
		t.Fatalf("regression precondition: step order/timestamps = %+v, %v", steps, err)
	}
	if err := sched.NewWorkflowOrchestrator(store, newMockExecutor(), nil, nil, nil).AdvanceWorkflowRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	final, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]string
	if err := json.Unmarshal(final.Output, &output); err != nil {
		t.Fatal(err)
	}
	if final.Status != state.WorkflowRunStatusSucceeded || output["step"] != "end" {
		t.Fatalf("run = %s/%s, want succeeded/end", final.Status, final.Output)
	}
}

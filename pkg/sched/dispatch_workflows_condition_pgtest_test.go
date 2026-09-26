package sched_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowOrchestrator_PgConditionAttemptExhaustionRunsTimeoutBranch(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "wf-pg-condition@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "wf-pg-condition", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.WorkflowSpec{Name: "condition", Steps: []api.WorkflowStepSpec{
		{Name: "await", WaitForCondition: &api.WorkflowConditionSpec{Run: "check_delivery", Interval: time.Hour, MaxAttempts: 1}, Timeout: 2 * time.Hour, OnTimeout: "notify"},
		{Name: "notify", Run: "notify"},
	}}
	snapshot, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, DefinitionSnapshot: snapshot}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	created, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if delay := time.Until(created.ScheduledFor); delay > 0 {
		time.Sleep(delay + 10*time.Millisecond)
	}
	executor := &conditionStepExecutor{results: [][]byte{[]byte(`{"done":false}`)}}
	if err := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil).DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	final, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil || final.Status != state.WorkflowRunStatusSucceeded || executor.checks != 1 {
		t.Fatalf("timeout branch = status:%s checks:%d err:%v", final.Status, executor.checks, err)
	}
}

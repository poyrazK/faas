// adr: 639
package sched_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowScheduleCatchUpExecutesOnceAfterSchedulerRestart(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "recovery-loop@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "recovery-loop", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:recovery",
		Workflows: json.RawMessage(`[{"name":"report","trigger":{"type":"schedule","schedule":"*/5 * * * *","catch_up":"latest","input":{"report":"daily"}},"steps":[{"name":"generate","run":"report"}]}]`)})
	if err != nil {
		t.Fatal(err)
	}
	engine, _ := sched.MakeEngine(t, store, &sched.FakeWakeVMM{})
	now := time.Now().UTC().Truncate(time.Hour).Add(-2*time.Hour + 30*time.Second)
	loop := sched.NewLoop(nil, engine, slog.Default()).WithWorkflowsDispatched(true).WithClock(func() time.Time { return now })
	if err := loop.RunWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(17 * time.Minute)
	for range 3 {
		restarted := sched.NewLoop(nil, engine, slog.Default()).WithWorkflowsDispatched(true).WithClock(func() time.Time { return now })
		if err := restarted.RunWorkflowSchedulesTick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	runs, total, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10})
	if err != nil || total != 1 || len(runs) != 1 || !runs[0].ScheduledFor.Equal(now.Truncate(time.Hour).Add(15*time.Minute)) {
		t.Fatalf("runs=%+v total=%d err=%v", runs, total, err)
	}
	executor := &sched.ScheduleStepExecutor{}
	orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, slog.Default())
	if err := orchestrator.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	completed, err := store.GetWorkflowRun(ctx, runs[0].ID)
	if err != nil || completed.Status != state.WorkflowRunStatusSucceeded || (*executor.Calls()) != 1 || string((*executor.Input())) != `{"report":"daily"}` {
		t.Fatalf("completed=%+v calls=%d input=%s err=%v", completed, (*executor.Calls()), (*executor.Input()), err)
	}
	history, err := store.ListWorkflowScheduleOccurrences(ctx, app.ID, "", "", 100)
	if err != nil || len(history) != 1 || !history[0].EvaluatedAt.Equal(now) {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}

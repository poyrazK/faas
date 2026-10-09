// adr: 830
package sched

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowTickEvaluatesFailurePoliciesBeforeScheduleAdmission(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "failure-tick@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "failure-tick", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`[{"name":"report","trigger":{"type":"schedule","schedule":"* * * * *"},"steps":[{"name":"main","path":"/report"}]}]`)
	if _, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:abc", Workflows: raw}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err = store.SetAutomationFailurePolicy(ctx, app.ID, "report", api.SetAutomationFailurePolicyRequest{Enabled: &enabled, FailureThreshold: 1, MinCompletedRuns: 1, WindowSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "report", DefinitionSnapshot: json.RawMessage(`{"name":"report","steps":[{"name":"main","path":"/report"}]}`)}
	if err = store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusFailed, nil, nil); err != nil {
		t.Fatal(err)
	}
	engine, _ := makeEngine(t, store, &fakeWakeVMM{})
	now := time.Now().UTC()
	loop := NewLoop(nil, engine, nil).WithClock(func() time.Time { return now }).WithWorkflowsDispatched(true)
	if err = loop.runWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := store.GetAutomationFailurePolicy(ctx, app.ID, "report")
	if err != nil || !out.Paused || out.Generation != 1 {
		t.Fatalf("scheduler failed to latch pause: %+v %v", out, err)
	}
	if cursors, err := store.ListWorkflowScheduleCursors(ctx, app.ID); err != nil || len(cursors) != 0 {
		t.Fatalf("paused workflow was armed: %+v %v", cursors, err)
	}
}

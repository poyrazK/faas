// adr: 640
package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type pinnedHandlerExecutor struct {
	store           state.Store
	deployments     []string
	identities      []WorkflowStepIdentity
	checks, retries int
}

func (e *pinnedHandlerExecutor) ExecuteStep(context.Context, string, string, string, map[string]string, []byte, time.Duration) (int, []byte, error) {
	return 0, nil, fmt.Errorf("unscoped execution must not be used")
}
func (e *pinnedHandlerExecutor) ExecuteWorkflowStep(ctx context.Context, appID string, identity WorkflowStepIdentity, path, method string, headers map[string]string, input []byte, timeout time.Duration, _ string, _ int64) (int, []byte, error) {
	_, version, err := state.ResolveInvocationVersion(ctx, e.store, state.Invocation{AppID: appID, Source: "workflow", WorkflowRunID: identity.RunID, PlatformTenantID: identity.PlatformTenantID})
	if err != nil {
		return 0, nil, err
	}
	e.deployments = append(e.deployments, version.DeploymentID)
	e.identities = append(e.identities, identity)
	switch path {
	case "/check":
		e.checks++
		if e.checks == 1 {
			return 200, []byte(`{"done":false}`), nil
		}
		return 200, []byte(`{"done":true}`), nil
	case "/retry":
		e.retries++
		if e.retries == 1 {
			return 503, nil, nil
		}
	}
	return 200, []byte(`{"ok":true}`), nil
}

func TestWorkflowDeploymentPinSurvivesConditionTimerLoopAndRetry(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "pinned-workflow@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "pinned-workflow", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:old", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	spec := api.WorkflowSpec{Name: "pinned", Steps: []api.WorkflowStepSpec{
		{Name: "condition", WaitForCondition: &api.WorkflowConditionSpec{Run: "check", Interval: 25 * time.Millisecond, MaxAttempts: 3}, Timeout: time.Second},
		{Name: "timer", WaitForDuration: 25 * time.Millisecond, DependsOn: []string{"condition"}},
		{Name: "items", DependsOn: []string{"timer"}, ForEach: &api.WorkflowForEachSpec{Items: "input.items", Action: api.WorkflowForEachActionSpec{Run: "item"}}},
		{Name: "retry", Run: "retry", DependsOn: []string{"items"}, Retry: &api.WorkflowRetrySpec{MaxAttempts: 2}},
	}}
	raw, _ := json.Marshal(spec)
	run := &state.WorkflowRun{AppID: app.ID, DeploymentID: old.ID, WorkflowName: spec.Name, Input: json.RawMessage(`{"items":[1,2]}`), DefinitionSnapshot: raw}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	executor := &pinnedHandlerExecutor{store: store}
	if err := NewWorkflowOrchestrator(store, executor, nil, nil, nil).DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	parked, _ := store.GetWorkflowRun(ctx, run.ID)
	if parked.Status != state.WorkflowRunStatusAwaitingEvent {
		t.Fatalf("condition did not park: %+v", parked)
	}
	newer, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:new", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, newer.ID); err != nil {
		t.Fatal(err)
	}
	// A fresh orchestrator on every wake exercises recovery from persisted rows.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := store.GetWorkflowRun(ctx, run.ID)
		if current.Status == state.WorkflowRunStatusSucceeded {
			break
		}
		if current.Status == state.WorkflowRunStatusDead || current.Status == state.WorkflowRunStatusFailed {
			t.Fatalf("failed: %+v", current)
		}
		if delay := time.Until(current.ScheduledFor); delay > 0 {
			time.Sleep(delay + 5*time.Millisecond)
		}
		if err := NewWorkflowOrchestrator(store, executor, nil, nil, nil).DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	final, _ := store.GetWorkflowRun(ctx, run.ID)
	if final.Status != state.WorkflowRunStatusSucceeded || executor.checks != 2 || executor.retries != 2 || len(executor.deployments) != 6 {
		t.Fatalf("final=%+v checks=%d retries=%d calls=%d", final, executor.checks, executor.retries, len(executor.deployments))
	}
	for i, id := range executor.deployments {
		if id != old.ID || executor.identities[i].DeploymentID != old.ID || executor.identities[i].RunID != run.ID {
			t.Fatalf("handler moved to another version: %s identity=%+v", id, executor.identities[i])
		}
	}
}

func TestWorkflowDeploymentPinRejectsLegacyExecutor(t *testing.T) {
	store := state.NewMemStore()
	executor := &foreachExecutor{}
	orchestrator := NewWorkflowOrchestrator(store, executor, nil, nil, nil)
	run := &state.WorkflowRun{AppID: "app", DeploymentID: "pinned", ID: "run"}
	if _, _, err := orchestrator.executeWorkflowHandler(t.Context(), run, "/handler", "POST", nil, nil, time.Second, "", 0); err == nil {
		t.Fatal("pinned run used executor without durable identity")
	}
	if len(executor.inputs) != 0 {
		t.Fatal("handler called after identity failure")
	}
}

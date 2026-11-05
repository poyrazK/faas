package sched_test

// adr: 905

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

var errPolicyUnavailable = errors.New("policy evaluation unavailable")

type failurePolicyScheduleFixture struct {
	MemStore *state.MemStore
	admitted map[string]int
}

func (f *failurePolicyScheduleFixture) EvaluateAutomationFailurePolicies(context.Context, string, string, int, time.Time) (string, error) {
	return "", errPolicyUnavailable
}

func (f *failurePolicyScheduleFixture) CheckAutomationFailureAdmission(_ context.Context, appID, _ string, _ time.Time) (bool, error) {
	switch appID {
	case "unavailable":
		return false, errPolicyUnavailable
	case "over-limit":
		return false, nil
	case "paused":
		return false, nil
	default:
		return true, nil
	}
}

func (f *failurePolicyScheduleFixture) ListWorkflowScheduleCandidates(context.Context, string, string, int) ([]state.WorkflowScheduleCandidate, error) {
	var rows []state.WorkflowScheduleCandidate
	for _, id := range []string{"healthy", "over-limit", "paused", "unavailable"} {
		rows = append(rows, state.WorkflowScheduleCandidate{AppID: id, Workflows: json.RawMessage(`[{"name":"report","trigger":{"type":"schedule"},"steps":[{"name":"main","path":"/report"}]}]`)})
	}
	return rows, nil
}

func (f *failurePolicyScheduleFixture) AdmitScheduledWorkflow(_ context.Context, appID, _, _ string, _ time.Time) (state.WorkflowScheduleCursor, bool, error) {
	f.admitted[appID]++
	return state.WorkflowScheduleCursor{}, false, nil
}

func TestFailurePolicyScanErrorDoesNotBlockHealthySchedules(t *testing.T) {
	fixture := &failurePolicyScheduleFixture{MemStore: state.NewMemStore(), admitted: map[string]int{}}
	engine, _ := sched.MakeEngine(t, statetest.NewMem(fixture), &sched.FakeWakeVMM{})
	loop := sched.NewLoop(nil, engine, nil).WithWorkflowsDispatched(true)
	err := loop.RunWorkflowSchedulesTick(t.Context())
	if !errors.Is(err, errPolicyUnavailable) {
		t.Fatalf("policy error not reported: %v", err)
	}
	if len(fixture.admitted) != 1 || fixture.admitted["healthy"] != 1 {
		t.Fatalf("admitted schedules: %v", fixture.admitted)
	}
}

type unavailablePolicyScan struct{ MemStore *state.MemStore }

func (*unavailablePolicyScan) EvaluateAutomationFailurePolicies(context.Context, string, string, int, time.Time) (string, error) {
	return "", errPolicyUnavailable
}

func TestFailurePolicyFallbackLatchesThreshold(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "fallback-latch@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "fallback-latch", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	definition := json.RawMessage(`[{"name":"nightly","trigger":{"type":"schedule","schedule":"* * * * *"},"steps":[{"name":"main","path":"/report"}]}]`)
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: "sha256:abc", Workflows: definition})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := store.SetAutomationFailurePolicy(ctx, app.ID, "nightly", api.SetAutomationFailurePolicyRequest{Enabled: &enabled, FailureThreshold: 1, MinCompletedRuns: 1, WindowSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "nightly", DefinitionSnapshot: json.RawMessage(`{"name":"nightly","steps":[{"name":"main","path":"/report"}]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusFailed, nil, nil); err != nil {
		t.Fatal(err)
	}
	engine, _ := sched.MakeEngine(t, statetest.NewMem(&unavailablePolicyScan{MemStore: store}), &sched.FakeWakeVMM{})
	loop := sched.NewLoop(nil, engine, nil).WithWorkflowsDispatched(true)
	if err := loop.RunWorkflowSchedulesTick(ctx); !errors.Is(err, errPolicyUnavailable) {
		t.Fatalf("scan failure: %v", err)
	}
	policy, err := store.GetAutomationFailurePolicy(ctx, app.ID, "nightly")
	if err != nil || !policy.Paused || policy.Generation != 1 {
		t.Fatalf("fallback did not persist pause: %+v, %v", policy, err)
	}
}

type partialFailurePolicyFixture struct {
	MemStore   *state.MemStore
	evaluated  []string
	checkCount int
}

func (f *partialFailurePolicyFixture) EvaluateAutomationFailurePolicies(_ context.Context, _, after string, _ int, _ time.Time) (string, error) {
	f.evaluated = append(f.evaluated, after)
	if len(f.evaluated) == 1 {
		return "next-page", &state.AutomationFailurePolicyBatchError{Err: errPolicyUnavailable}
	}
	return "", nil
}

func (f *partialFailurePolicyFixture) CheckAutomationFailureAdmission(context.Context, string, string, time.Time) (bool, error) {
	f.checkCount++
	return true, nil
}

func (f *partialFailurePolicyFixture) ListWorkflowScheduleCandidates(context.Context, string, string, int) ([]state.WorkflowScheduleCandidate, error) {
	return []state.WorkflowScheduleCandidate{{AppID: "healthy", Workflows: json.RawMessage(`[{"name":"report","trigger":{"type":"schedule"},"steps":[{"name":"main","path":"/report"}]}]`)}}, nil
}

func (f *partialFailurePolicyFixture) AdmitScheduledWorkflow(context.Context, string, string, string, time.Time) (state.WorkflowScheduleCursor, bool, error) {
	return state.WorkflowScheduleCursor{}, false, nil
}

func TestFailurePolicyScanContinuesAndChecksUntilCleanPass(t *testing.T) {
	fixture := &partialFailurePolicyFixture{MemStore: state.NewMemStore()}
	engine, _ := sched.MakeEngine(t, statetest.NewMem(fixture), &sched.FakeWakeVMM{})
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	loop := sched.NewLoop(nil, engine, nil).WithWorkflowsDispatched(true).WithClock(func() time.Time { return now })
	if err := loop.RunWorkflowSchedulesTick(t.Context()); !errors.Is(err, errPolicyUnavailable) {
		t.Fatalf("expected partial failure: %v", err)
	}
	now = now.Add(time.Minute)
	if err := loop.RunWorkflowSchedulesTick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(fixture.evaluated) != 2 || fixture.evaluated[1] != "next-page" || fixture.checkCount != 2 {
		t.Fatalf("page progress %v, fallback checks %d", fixture.evaluated, fixture.checkCount)
	}
	now = now.Add(time.Minute)
	if err := loop.RunWorkflowSchedulesTick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(fixture.evaluated) != 3 || fixture.evaluated[2] != "" || fixture.checkCount != 2 {
		t.Fatalf("clean pass %v, fallback checks %d", fixture.evaluated, fixture.checkCount)
	}
}

type tenantPolicyCacheFixture struct {
	MemStore *state.MemStore
	checks   int
}

func (f *tenantPolicyCacheFixture) EvaluateAutomationFailurePolicies(context.Context, string, string, int, time.Time) (string, error) {
	return "", errPolicyUnavailable
}

func (f *tenantPolicyCacheFixture) CheckAutomationFailureAdmission(context.Context, string, string, time.Time) (bool, error) {
	f.checks++
	return true, nil
}

func TestFailurePolicyFallbackCachesTenantChecks(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "policy-cache@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "policy-cache", Type: state.AppTypeApp, RAMMB: 256, PlatformTenantRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	definition := json.RawMessage(`[{"name":"report","trigger":{"type":"schedule","schedule":"* * * * *"},"steps":[{"name":"main","path":"/report"}]}]`)
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:abc", Workflows: definition}); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		ref := fmt.Sprintf("policy-cache-%d", i)
		tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, ref, ref, 10)
		if err != nil {
			t.Fatal(err)
		}
		consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, ref, ref)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil {
			t.Fatal(err)
		}
	}
	fixture := &tenantPolicyCacheFixture{MemStore: store}
	engine, _ := sched.MakeEngine(t, statetest.NewMem(fixture), &sched.FakeWakeVMM{})
	loop := sched.NewLoop(nil, engine, nil).WithWorkflowsDispatched(true)
	if err := loop.RunWorkflowSchedulesTick(ctx); !errors.Is(err, errPolicyUnavailable) {
		t.Fatalf("expected policy scan error: %v", err)
	}
	if fixture.checks != 1 {
		t.Fatalf("policy reads = %d; want one for four tenant candidates", fixture.checks)
	}
}

type slowPolicyScanFixture struct {
	MemStore *state.MemStore
	admitted bool
}

func (f *slowPolicyScanFixture) EvaluateAutomationFailurePolicies(ctx context.Context, _, _ string, _ int, _ time.Time) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func (f *slowPolicyScanFixture) CheckAutomationFailureAdmission(context.Context, string, string, time.Time) (bool, error) {
	return true, nil
}
func (f *slowPolicyScanFixture) ListWorkflowScheduleCandidates(context.Context, string, string, int) ([]state.WorkflowScheduleCandidate, error) {
	return []state.WorkflowScheduleCandidate{{AppID: "healthy", Workflows: json.RawMessage(`[{"name":"report","trigger":{"type":"schedule"}}]`)}}, nil
}
func (f *slowPolicyScanFixture) AdmitScheduledWorkflow(context.Context, string, string, string, time.Time) (state.WorkflowScheduleCursor, bool, error) {
	f.admitted = true
	return state.WorkflowScheduleCursor{}, false, nil
}
func TestSlowPolicyScanDoesNotBlockHealthySchedules(t *testing.T) {
	fixture := &slowPolicyScanFixture{MemStore: state.NewMemStore()}
	engine, _ := sched.MakeEngine(t, statetest.NewMem(fixture), &sched.FakeWakeVMM{})
	loop := sched.NewLoop(nil, engine, nil).WithWorkflowsDispatched(true)
	started := time.Now()
	err := loop.RunWorkflowSchedulesTick(t.Context())
	if !errors.Is(err, context.DeadlineExceeded) || !fixture.admitted {
		t.Fatalf("admitted=%v error=%v", fixture.admitted, err)
	}
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Fatalf("policy timeout took %s", elapsed)
	}
}

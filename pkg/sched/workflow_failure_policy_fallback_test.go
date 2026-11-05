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

func (f *failurePolicyScheduleFixture) GetAutomationFailurePolicy(_ context.Context, appID, _ string) (api.AutomationFailurePolicyResponse, error) {
	switch appID {
	case "unavailable":
		return api.AutomationFailurePolicyResponse{}, errPolicyUnavailable
	case "over-limit":
		return api.AutomationFailurePolicyResponse{Policy: api.AutomationFailurePolicy{Enabled: true, FailureThreshold: 2, MinCompletedRuns: 3}, ObservedFailures: 2, ObservedCompletedRuns: 3}, nil
	case "paused":
		return api.AutomationFailurePolicyResponse{Paused: true}, nil
	default:
		return api.AutomationFailurePolicyResponse{}, nil
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

func (f *partialFailurePolicyFixture) GetAutomationFailurePolicy(context.Context, string, string) (api.AutomationFailurePolicyResponse, error) {
	f.checkCount++
	return api.AutomationFailurePolicyResponse{}, nil
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

func (f *tenantPolicyCacheFixture) GetAutomationFailurePolicy(context.Context, string, string) (api.AutomationFailurePolicyResponse, error) {
	f.checks++
	return api.AutomationFailurePolicyResponse{}, nil
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
func (f *slowPolicyScanFixture) GetAutomationFailurePolicy(context.Context, string, string) (api.AutomationFailurePolicyResponse, error) {
	return api.AutomationFailurePolicyResponse{}, nil
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

// adr: 487
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type scheduleStepExecutor struct {
	calls int
	input []byte
}

func (e *scheduleStepExecutor) ExecuteStep(_ context.Context, _ string, path, _ string, _ map[string]string, input []byte, _ time.Duration) (int, []byte, error) {
	e.calls++
	e.input = append([]byte(nil), input...)
	return 200, []byte(`{"report":"sent"}`), nil
}

func (e *scheduleStepExecutor) ExecuteWorkflowStep(ctx context.Context, appID string, identity WorkflowStepIdentity, path, method string, headers map[string]string, input []byte, timeout time.Duration, _ string, _ int64) (int, []byte, error) {
	return e.ExecuteStep(ctx, appID, path, method, headers, input, timeout)
}

func TestWorkflowScheduleStartsAndExecutesWithoutAdapter(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "schedule-loop@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "schedule-loop", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	definitions := json.RawMessage(`[{"name":"report","trigger":{"type":"schedule","schedule":"0 7 * * *","timezone":"Europe/Istanbul","input":{"day":"today"}},"steps":[{"name":"generate","run":"report"}]}]`)
	_, err = store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:abc", Workflows: definitions})
	if err != nil {
		t.Fatal(err)
	}
	engine, _ := makeEngine(t, store, &fakeWakeVMM{})
	now := time.Date(2026, 10, 2, 3, 59, 30, 0, time.UTC)
	loop := NewLoop(nil, engine, slog.Default()).WithClock(func() time.Time { return now })
	if err := loop.runWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	if cursors, _ := store.ListWorkflowScheduleCursors(ctx, app.ID); len(cursors) != 0 {
		t.Fatal("disabled runtime armed a schedule")
	}
	loop.WithWorkflowsDispatched(true)
	if err := loop.runWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := loop.runWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	// Another schedd process observing the same boundary must not duplicate it.
	restarted := NewLoop(nil, engine, slog.Default()).WithWorkflowsDispatched(true).WithClock(func() time.Time { return now })
	if err := restarted.runWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	runs, total, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10})
	if err != nil || total != 1 {
		t.Fatalf("runs=%d err=%v", total, err)
	}
	executor := &scheduleStepExecutor{}
	orchestrator := NewWorkflowOrchestrator(store, executor, nil, nil, slog.Default())
	if err := orchestrator.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	completed, err := store.GetWorkflowRun(ctx, runs[0].ID)
	if err != nil || completed.Status != state.WorkflowRunStatusSucceeded || executor.calls != 1 || string(executor.input) != `{"day":"today"}` {
		t.Fatalf("run=%+v calls=%d input=%s err=%v", completed, executor.calls, executor.input, err)
	}
}

func TestTenantWorkflowSchedulesRunPerActiveTenantLink(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "tenant-schedule-loop@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "tenant-schedule-loop",
		Type: state.AppTypeApp, RAMMB: 256, PlatformTenantRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	definitions := json.RawMessage(`[{"name":"report","trigger":{"type":"schedule","schedule":"* * * * *","timezone":"UTC","overlap":"skip"},"steps":[{"name":"generate","run":"report"}]}]`)
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
		Status: state.DeployPending, ImageDigest: "sha256:abc", Workflows: definitions})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	tenantStore := store
	var tenantIDs []string
	for i := range 2 {
		ref := fmt.Sprintf("schedule-customer-%d", i)
		tenant, _, err := tenantStore.CreatePlatformTenant(ctx, account.ID, ref, ref, 10)
		if err != nil {
			t.Fatal(err)
		}
		consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, ref, ref)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tenantStore.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil {
			t.Fatal(err)
		}
		tenantIDs = append(tenantIDs, tenant.ID)
	}
	engine, _ := makeEngine(t, store, &fakeWakeVMM{})
	now := time.Date(2026, 10, 6, 10, 0, 30, 0, time.UTC)
	loop := NewLoop(nil, engine, slog.Default()).WithWorkflowsDispatched(true).WithClock(func() time.Time { return now })
	if err := loop.runWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := loop.runWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	// A second scheduler at the same minute observes durable tenant cursors.
	restarted := NewLoop(nil, engine, slog.Default()).WithWorkflowsDispatched(true).WithClock(func() time.Time { return now })
	if err := restarted.runWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	runs, total, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10})
	if err != nil || total != 2 || len(runs) != 2 {
		t.Fatalf("tenant scheduled runs=%+v total=%d err=%v", runs, total, err)
	}
	seen := map[string]bool{}
	for _, run := range runs {
		seen[run.PlatformTenantID] = true
	}
	if !seen[tenantIDs[0]] || !seen[tenantIDs[1]] {
		t.Fatalf("runs did not preserve each tenant identity: %+v", seen)
	}
}

func TestWorkflowSchedulesHaveCapacityWhileHandlersAreBusy(t *testing.T) {
	pool := newWorkPool(quietLog(), nil)
	release := fillSlots(t, pool, workWorkflowDispatch)
	defer release()
	done := make(chan struct{})
	if result := pool.submit(workWorkflowSchedules, "tick", func() { close(done) }); result != "queued" {
		t.Fatalf("schedule work while handlers busy = %s", result)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("schedule evaluation was blocked by workflow handlers")
	}
}

type pagedScheduleStore struct {
	*state.MemStore
	candidates []state.WorkflowScheduleCandidate
	seen       map[string]int
	queries    int
}

func (s *pagedScheduleStore) ListWorkflowScheduleCandidates(_ context.Context, _, after string, limit int) ([]state.WorkflowScheduleCandidate, error) {
	s.queries++
	var page []state.WorkflowScheduleCandidate
	for _, candidate := range s.candidates {
		if candidate.AppID > after {
			page = append(page, candidate)
			if len(page) == limit {
				break
			}
		}
	}
	return page, nil
}

func (s *pagedScheduleStore) AdmitScheduledWorkflow(_ context.Context, appID, _, _ string, _ time.Time) (state.WorkflowScheduleCursor, bool, error) {
	s.seen[appID]++
	if appID == s.candidates[0].AppID && s.seen[appID] == 1 {
		return state.WorkflowScheduleCursor{}, false, errors.New("transient admission failure")
	}
	return state.WorkflowScheduleCursor{}, false, nil
}

func TestWorkflowScheduleRetriesWithoutStarvingLaterPages(t *testing.T) {
	store := &pagedScheduleStore{MemStore: state.NewMemStore(), seen: map[string]int{}}
	for i := range workflowScheduleBatch + 1 {
		store.candidates = append(store.candidates, state.WorkflowScheduleCandidate{AppID: fmt.Sprintf("app-%04d", i),
			Workflows: json.RawMessage(`[{"name":"report","trigger":{"type":"schedule","schedule":"* * * * *"},"steps":[{"name":"main","run":"report"}]}]`)})
	}
	engine, _ := makeEngine(t, store, &fakeWakeVMM{})
	now := time.Date(2026, 10, 2, 10, 0, 30, 0, time.UTC)
	loop := NewLoop(nil, engine, quietLog()).WithWorkflowsDispatched(true).WithClock(func() time.Time { return now })
	ctx := context.Background()
	if err := loop.runWorkflowSchedulesTick(ctx); err == nil {
		t.Fatal("expected first-page failure")
	}
	if err := loop.runWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(store.seen) != len(store.candidates) {
		t.Fatal("failed admission starved the later page")
	}
	for range 2 {
		if err := loop.runWorkflowSchedulesTick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if store.seen[store.candidates[0].AppID] != 2 {
		t.Fatal("failed admission was not retried")
	}
	if err := loop.runWorkflowSchedulesTick(ctx); err != nil {
		t.Fatal(err)
	}
	if store.queries != 4 {
		t.Fatal("completed minute was scanned again")
	}
}

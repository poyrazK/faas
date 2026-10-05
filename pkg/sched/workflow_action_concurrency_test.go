package sched

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type actionCapacityExecutor struct {
	calls        atomic.Int32
	entered      chan string
	releaseFirst chan struct{}
}

func (e *actionCapacityExecutor) ExecuteStep(ctx context.Context, _, _, _ string, headers map[string]string, input []byte, _ time.Duration) (int, []byte, error) {
	runID := headers["X-Faas-Workflow-Run-Id"]
	call := e.calls.Add(1)
	e.entered <- runID
	if call == 1 {
		select {
		case <-e.releaseFirst:
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		}
	}
	return 200, append([]byte(nil), input...), nil
}

func TestWorkflowActionConcurrencyQueuesAndResumesAnotherRun(t *testing.T) {
	guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "action-cap-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256})
		if err != nil {
			t.Fatal(err)
		}
		spec := api.WorkflowSpec{
			Name:                 "bounded-actions",
			MaxConcurrentActions: 1,
			Steps:                []api.WorkflowStepSpec{{Name: "send", Run: "send"}},
		}
		snapshot, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		runs := make([]*state.WorkflowRun, 2)
		for index := range runs {
			scheduledFor := time.Now().UTC().Add(-time.Second + time.Duration(index)*time.Second)
			runs[index] = &state.WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, Input: json.RawMessage(`{}`), DefinitionSnapshot: snapshot, ScheduledFor: scheduledFor}
			if err := store.CreateWorkflowRun(ctx, runs[index]); err != nil {
				t.Fatal(err)
			}
		}
		executor := &actionCapacityExecutor{entered: make(chan string, 2), releaseFirst: make(chan struct{})}
		var releaseOnce sync.Once
		releaseFirst := func() { releaseOnce.Do(func() { close(executor.releaseFirst) }) }
		defer releaseFirst()
		orchestrator := NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		firstDone := make(chan error, 1)
		go func() { firstDone <- orchestrator.DispatchTick(ctx) }()
		select {
		case runID := <-executor.entered:
			if runID != runs[0].ID {
				t.Fatalf("first claimed run = %s, want %s", runID, runs[0].ID)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("first action did not start")
		}

		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		queued, err := store.GetWorkflowRun(ctx, runs[1].ID)
		if err != nil || queued.Status != state.WorkflowRunStatusPending || !queued.ScheduledFor.After(time.Now().UTC()) {
			t.Fatalf("action-cap run was not durably queued: run=%+v err=%v", queued, err)
		}
		if got := executor.calls.Load(); got != 1 {
			t.Fatalf("executor received %d calls while one action held the only slot", got)
		}

		releaseFirst()
		if err := <-firstDone; err != nil {
			t.Fatal(err)
		}
		if delay := time.Until(queued.ScheduledFor); delay > 0 {
			time.Sleep(delay)
		}
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		completed, err := store.GetWorkflowRun(ctx, runs[1].ID)
		if err != nil || completed.Status != state.WorkflowRunStatusSucceeded || executor.calls.Load() != 2 {
			t.Fatalf("queued action did not resume: run=%+v calls=%d err=%v", completed, executor.calls.Load(), err)
		}
	})
}

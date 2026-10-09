// adr: 641
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fairDispatchExecutor struct {
	entered chan string
	release chan struct{}
	calls   atomic.Int32
}

func (e *fairDispatchExecutor) ExecuteStep(ctx context.Context, appID, _, _ string, _ map[string]string, _ []byte, _ time.Duration) (int, []byte, error) {
	e.calls.Add(1)
	if e.entered != nil {
		e.entered <- appID
	}
	if e.release != nil {
		select {
		case <-e.release:
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		}
	}
	return 200, []byte(`{}`), nil
}

func seedDispatchRuns(t *testing.T, store state.Store, appID string, count int, due time.Time) []*state.WorkflowRun {
	t.Helper()
	var runs []*state.WorkflowRun
	for index := range count {
		run := &state.WorkflowRun{AppID: appID, WorkflowName: "work", ScheduledFor: due.Add(time.Duration(index) * time.Millisecond),
			DefinitionSnapshot: json.RawMessage(`{"name":"work","steps":[{"name":"work","run":"work"}]}`)}
		if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
	}
	return runs
}

func waitDispatchEntries(t *testing.T, executor *fairDispatchExecutor, count int) map[string]int {
	t.Helper()
	apps := make(map[string]int)
	for range count {
		select {
		case appID := <-executor.entered:
			apps[appID]++
		case <-time.After(5 * time.Second):
			t.Fatal("available workflow slot did not start")
		}
	}
	return apps
}

func TestWorkflowDispatchFillsSlotsAndCoalescesBusyTicks(t *testing.T) {
	store := state.NewMemStore()
	base := time.Now().Add(-time.Hour)
	var all []*state.WorkflowRun
	for _, appID := range []string{"hot", "second", "third", "fourth"} {
		all = append(all, seedDispatchRuns(t, store, appID, 2, base)...)
		base = base.Add(time.Minute)
	}
	executor := &fairDispatchExecutor{entered: make(chan string, len(all)), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	loop := &Loop{engine: &Engine{store: store}, log: quietLog(), workflowOrch: NewWorkflowOrchestrator(store, executor, nil, nil, quietLog())}
	t.Cleanup(func() { cancel(); loop.workPool().drain() })
	loop.runWorkflowsDispatchTick(ctx)
	apps := waitDispatchEntries(t, executor, api.WorkflowDispatchSlots)
	if len(apps) != api.WorkflowDispatchSlots {
		t.Fatalf("backlog monopolized slots: %+v", apps)
	}
	for range 20 {
		loop.runWorkflowsDispatchTick(ctx)
	}
	if calls := executor.calls.Load(); calls != int32(api.WorkflowDispatchSlots) {
		t.Fatalf("busy ticks started %d handlers", calls)
	}
	pending := 0
	for _, run := range all {
		current, err := store.GetWorkflowRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == state.WorkflowRunStatusPending {
			pending++
		}
	}
	if pending != len(all)-api.WorkflowDispatchSlots {
		t.Fatalf("claimed work without a slot: pending=%d", pending)
	}
	close(executor.release)
	loop.workPool().drain()
	for _, run := range all {
		current, err := store.GetWorkflowRun(ctx, run.ID)
		if err != nil || current.Status != state.WorkflowRunStatusSucceeded {
			t.Fatalf("bounded drain did not finish run: %+v err=%v", current, err)
		}
	}
}

func TestWorkflowDispatchSlowAppLeavesCapacityForNewApp(t *testing.T) {
	store := state.NewMemStore()
	seedDispatchRuns(t, store, "slow", 10, time.Now().Add(-time.Hour))
	executor := &fairDispatchExecutor{entered: make(chan string, 20), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	loop := &Loop{engine: &Engine{store: store}, log: quietLog(), workflowOrch: NewWorkflowOrchestrator(store, executor, nil, nil, quietLog())}
	t.Cleanup(func() { cancel(); loop.workPool().drain() })
	loop.runWorkflowsDispatchTick(ctx)
	waitDispatchEntries(t, executor, api.WorkflowDispatchMaxPerApp)
	seedDispatchRuns(t, store, "new", 1, time.Now().Add(-time.Minute))
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticks := time.NewTicker(time.Millisecond)
	defer ticks.Stop()
	for {
		select {
		case appID := <-executor.entered:
			if appID != "new" {
				t.Fatalf("slow app exceeded budget: %s", appID)
			}
			return
		case <-ticks.C:
			loop.runWorkflowsDispatchTick(ctx)
		case <-deadline.C:
			t.Fatal("new app blocked behind slow backlog")
		}
	}
}

func TestWorkflowDispatchBatchBoundAndCancellation(t *testing.T) {
	store := state.NewMemStore()
	runs := seedDispatchRuns(t, store, "fast", api.WorkflowDispatchBatchPerSlot+3, time.Now().Add(-time.Hour))
	executor := &fairDispatchExecutor{}
	orch := NewWorkflowOrchestrator(store, executor, nil, nil, nil)
	if err := orch.DispatchBatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls := executor.calls.Load(); calls != int32(api.WorkflowDispatchBatchPerSlot) {
		t.Fatalf("batch calls=%d", calls)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := orch.DispatchBatch(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled dispatch: %v", err)
	}
	for _, run := range runs[api.WorkflowDispatchBatchPerSlot:] {
		current, err := store.GetWorkflowRun(t.Context(), run.ID)
		if err != nil || current.Status != state.WorkflowRunStatusPending {
			t.Fatalf("cancelled batch claimed work: %+v err=%v", current, err)
		}
	}
	if err := orch.DispatchBatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls := executor.calls.Load(); calls != int32(len(runs)) {
		t.Fatalf("remaining calls=%d", calls)
	}
}

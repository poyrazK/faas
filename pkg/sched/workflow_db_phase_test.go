// adr: 20261025150000 — dispatch slots give back their database token while a
// step runs, so slow handlers no longer stall other customers' workflows.
package sched_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// concurrency tracks how many goroutines are inside a section at once.
type concurrency struct {
	now, peak atomic.Int32
}

func (c *concurrency) enter() {
	n := c.now.Add(1)
	for {
		peak := c.peak.Load()
		if n <= peak || c.peak.CompareAndSwap(peak, n) {
			return
		}
	}
}

func (c *concurrency) leave() { c.now.Add(-1) }

func TestWorkflowDBPhaseBoundsDatabaseWorkButNotHandlerCalls(t *testing.T) {
	const goroutines = 8
	orch := sched.NewWorkflowOrchestrator(state.NewMemStore(), nil, nil, nil, nil)
	orch.SetDBPhaseSlotsForTest(2)
	var database, calls concurrency
	allCalling := make(chan struct{})
	var calling atomic.Int32
	dbWork := func() {
		database.enter()
		time.Sleep(2 * time.Millisecond)
		database.leave()
	}
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			ctx, leave, err := orch.EnterDBPhaseForTest(t.Context())
			if err != nil {
				t.Error(err)
				return
			}
			defer leave()
			dbWork()
			sched.OutsideDBPhaseForTest(ctx, func() {
				calls.enter()
				defer calls.leave()
				if calling.Add(1) == goroutines {
					close(allCalling)
				}
				select {
				case <-allCalling:
				case <-time.After(5 * time.Second):
					t.Error("handler calls were held back by database tokens")
				}
			})
			dbWork()
		})
	}
	wg.Wait()
	if peak := database.peak.Load(); peak > 2 {
		t.Fatalf("database work ran %d at once, want at most 2", peak)
	}
	if peak := calls.peak.Load(); peak != goroutines {
		t.Fatalf("handler calls overlapped %d at once, want %d", peak, goroutines)
	}
}

func TestWorkflowDBPhaseChildrenTakeTheirOwnTokens(t *testing.T) {
	orch := sched.NewWorkflowOrchestrator(state.NewMemStore(), nil, nil, nil, nil)
	// One token: a parent that kept its token while waiting on its items
	// would deadlock here.
	orch.SetDBPhaseSlotsForTest(1)
	ctx, leave, err := orch.EnterDBPhaseForTest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer leave()
	var database concurrency
	done := make(chan struct{})
	go func() {
		defer close(done)
		sched.OutsideDBPhaseForTest(ctx, func() {
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() {
					itemCtx, leaveItem := orch.ChildDBPhaseForTest(ctx)
					defer leaveItem()
					database.enter()
					time.Sleep(time.Millisecond)
					database.leave()
					sched.OutsideDBPhaseForTest(itemCtx, func() { time.Sleep(time.Millisecond) })
					database.enter()
					database.leave()
				})
			}
			wg.Wait()
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("for_each items deadlocked on the parent's token")
	}
	if peak := database.peak.Load(); peak != 1 {
		t.Fatalf("items ran database work %d at once, want 1", peak)
	}
}

type parkedStepExecutor struct {
	inFlight concurrency
	entered  chan string
	release  chan struct{}
}

func (e *parkedStepExecutor) ExecuteStep(ctx context.Context, appID, _, _ string, _ map[string]string, _ []byte, _ time.Duration) (int, []byte, error) {
	e.inFlight.enter()
	defer e.inFlight.leave()
	e.entered <- appID
	select {
	case <-e.release:
		return 200, []byte(`{}`), nil
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

// H13-9: with one database token, three slots still wait on three slow
// handlers at once and then record every result.
func TestWorkflowDispatchSlowStepsDoNotHoldDatabaseTokens(t *testing.T) {
	store := state.NewMemStore()
	const apps = 3
	var runs []*state.WorkflowRun
	for index := range apps {
		run := &state.WorkflowRun{AppID: fmt.Sprintf("slow-%d", index), WorkflowName: "work",
			ScheduledFor:       time.Now().Add(-time.Minute),
			DefinitionSnapshot: json.RawMessage(`{"name":"work","steps":[{"name":"work","run":"work"}]}`)}
		if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
	}
	executor := &parkedStepExecutor{entered: make(chan string, apps), release: make(chan struct{})}
	orch := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
	orch.SetDBPhaseSlotsForTest(1)
	var wg sync.WaitGroup
	for range apps {
		wg.Go(func() {
			if err := orch.DispatchBatch(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	for range apps {
		select {
		case <-executor.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("a slot waited for a database token held across another slot's step")
		}
	}
	if peak := executor.inFlight.peak.Load(); peak != apps {
		t.Fatalf("slow steps in flight = %d, want %d", peak, apps)
	}
	close(executor.release)
	wg.Wait()
	for _, run := range runs {
		final, err := store.GetWorkflowRun(t.Context(), run.ID)
		if err != nil || final.Status != state.WorkflowRunStatusSucceeded {
			t.Fatalf("run %s = %+v err=%v, want succeeded", run.ID, final, err)
		}
	}
	if api.WorkflowDispatchSlots < 8*api.WorkflowDispatchMaxPerApp {
		t.Fatalf("WorkflowDispatchSlots=%d lets fewer than eight slow apps fill a scheduler", api.WorkflowDispatchSlots)
	}
}

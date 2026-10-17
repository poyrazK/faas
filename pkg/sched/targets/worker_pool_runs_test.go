// adr: 1420 — worker pools reconcile off the scheduler loop.
package targets

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// gatedWorkerPools blocks an app's reconcile until its gate closes, the way a
// worker boot holds a real reconcile for a characterization window.
type gatedWorkerPools struct {
	*workerPoolFakeEngine
	mu      sync.Mutex
	calls   []string
	started chan string
	gates   map[string]chan struct{}
}

func (e *gatedWorkerPools) ReconcileWorkerPools(_ context.Context, appID, _ string) error {
	e.mu.Lock()
	e.calls = append(e.calls, appID)
	gate := e.gates[appID]
	e.mu.Unlock()
	e.started <- appID
	if gate != nil {
		<-gate
	}
	return nil
}

func TestTrigger_WorkerPoolRunsLeaveTheTickAndSerializePerAccount(t *testing.T) {
	worker := func(id, account string) state.App {
		return state.App{ID: id, AccountID: account, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 3,
			ScalingPolicy: &state.ScalingPolicy{Target: &state.ScalingTarget{Metric: "queue_depth", Value: 1}}}
	}
	store := &fakeStore{apps: []state.App{worker("a1", "acct-a"), worker("a2", "acct-a"), worker("b1", "acct-b")}}
	release := make(chan struct{})
	engine := &gatedWorkerPools{workerPoolFakeEngine: &workerPoolFakeEngine{fakeEngine: &fakeEngine{}},
		started: make(chan string, 16), gates: map[string]chan struct{}{"a1": release}}
	queue := &fakeQueueStats{byApp: map[string]state.QueueStats{"a1": {Depth: 3}, "a2": {Depth: 3}, "b1": {Depth: 3}}}
	tr := New(store, nil, engine, nil, Options{QueueStatsReader: queue})
	tick := func() {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- tr.Tick(context.Background()) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Tick waited for a worker pool reconcile")
		}
	}
	// A blocked reconcile in account A neither holds the tick nor account B.
	tick()
	first := map[string]bool{}
	for len(first) < 2 {
		select {
		case id := <-engine.started:
			first[id] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("background reconciles started: %v, want a1 and b1", first)
		}
	}
	if !first["a1"] || !first["b1"] {
		t.Fatalf("first reconciles = %v, want a1 and b1", first)
	}
	// a2 waits behind a1 in account A's run, and a tick while that run is
	// going starts nothing more for A.
	tick()
	quiet := time.After(200 * time.Millisecond)
	for waiting := true; waiting; {
		select {
		case id := <-engine.started:
			if id != "b1" {
				t.Fatalf("%s reconciled while account A's run held a1", id)
			}
		case <-quiet:
			waiting = false
		}
	}
	close(release)
	select {
	case id := <-engine.started:
		if id != "a2" {
			t.Fatalf("after a1 returned, %s reconciled; want a2", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a2 never reconciled after a1 returned")
	}
	tr.waitWorkerPoolRuns()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	a := slices.DeleteFunc(slices.Clone(engine.calls), func(id string) bool { return id == "b1" })
	if !slices.Equal(a, []string{"a1", "a2"}) {
		t.Fatalf("account A reconciles = %v, want [a1 a2] in tick order", a)
	}
}

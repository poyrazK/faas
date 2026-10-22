// adr: 20261018101500 — a queue message wakes an idle worker pool.
package targets

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// lockedWorkerPools records reconciles from concurrent account runs.
type lockedWorkerPools struct {
	*workerPoolFakeEngine
	mu    sync.Mutex
	calls []string
	err   error
}

func (e *lockedWorkerPools) ReconcileWorkerPools(_ context.Context, appID, trigger string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, appID+":"+trigger)
	return e.err
}

// take returns the recorded reconciles and clears them.
func (e *lockedWorkerPools) take() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	calls := e.calls
	e.calls = nil
	return calls
}

func (e *lockedWorkerPools) fail(err error) {
	e.mu.Lock()
	e.err = err
	e.mu.Unlock()
}

func queueWorkerApp(id, account string) state.App {
	return state.App{ID: id, AccountID: account, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 3,
		ScalingPolicy: &state.ScalingPolicy{Target: &state.ScalingTarget{Metric: "queue_depth", Value: 1}}}
}

func TestTrigger_WakeWorkerPoolReconcilesOnlyAnIdleQueuePool(t *testing.T) {
	custom := state.App{ID: "custom", AccountID: "acct-c", WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 3,
		ScalingPolicy: &state.ScalingPolicy{Target: &state.ScalingTarget{Metric: "custom", Name: "tasks", Value: 2}}}
	store := &fakeStore{apps: []state.App{queueWorkerApp("idle", "acct-a"), queueWorkerApp("serving", "acct-b"), custom}}
	engine := &lockedWorkerPools{workerPoolFakeEngine: &workerPoolFakeEngine{fakeEngine: &fakeEngine{}}}
	ledger := &fakeLedger{conc: map[string]int{"serving": 1}}
	tr := New(store, nil, engine, ledger, Options{QueueStatsReader: &fakeQueueStats{}, CustomMetricReader: fixedCustomMetrics{}})
	ctx := context.Background()
	if tr.WakeWorkerPool(ctx, "idle") {
		t.Fatal("a wake before any tick started a run; only apps a tick saw are woken")
	}
	if err := tr.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	tr.waitWorkerPoolRuns()
	engine.take()

	for _, tc := range []struct {
		app  string
		want bool
	}{
		{"idle", true},
		{"serving", false}, // its worker leases the message
		{"custom", false},  // a queue message is not its demand
		{"unknown", false},
	} {
		if got := tr.WakeWorkerPool(ctx, tc.app); got != tc.want {
			t.Errorf("WakeWorkerPool(%s) = %v, want %v", tc.app, got, tc.want)
		}
	}
	tr.waitWorkerPoolRuns()
	if calls := engine.take(); !slices.Equal(calls, []string{"idle:worker.pool"}) {
		t.Fatalf("woken reconciles = %v, want [idle:worker.pool]", calls)
	}

	// A failed reconcile puts the app in admission backoff; a wake honors it.
	engine.fail(errors.New("admission failed"))
	if !tr.WakeWorkerPool(ctx, "idle") {
		t.Fatal("wake of the idle pool started no run")
	}
	tr.waitWorkerPoolRuns()
	if tr.WakeWorkerPool(ctx, "idle") {
		t.Fatal("a wake started a run during admission backoff")
	}
}

// A message can land while the account's run is freezing the pool's only
// worker: the row is FROZEN before the ledger releases the worker. The wake
// waits for that run and is checked again when it finishes.
func TestTrigger_WakeWorkerPoolDuringTheAccountsRunWaitsForIt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		serving int // the app's serving instances when the run finishes
		want    []string
	}{
		{name: "pool idle when the run finishes", want: []string{"w", "w"}},
		{name: "pool serving when the run finishes", serving: 1, want: []string{"w"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{apps: []state.App{queueWorkerApp("w", "acct")}}
			release := make(chan struct{})
			engine := &gatedWorkerPools{workerPoolFakeEngine: &workerPoolFakeEngine{fakeEngine: &fakeEngine{}},
				started: make(chan string, 4), gates: map[string]chan struct{}{"w": release}}
			ledger := &fakeLedger{conc: map[string]int{"w": 1}}
			tr := New(store, nil, engine, ledger, Options{QueueStatsReader: &fakeQueueStats{}})
			ctx := context.Background()
			if err := tr.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case <-engine.started:
			case <-time.After(5 * time.Second):
				t.Fatal("the tick started no reconcile")
			}
			// Mid-run the ledger still counts the worker being frozen.
			if tr.WakeWorkerPool(ctx, "w") {
				t.Fatal("a wake started a second run while the account's run was going")
			}
			ledger.mu.Lock()
			ledger.conc["w"] = tc.serving
			ledger.mu.Unlock()
			close(release)
			tr.waitWorkerPoolRuns()
			engine.mu.Lock()
			calls := slices.Clone(engine.calls)
			engine.mu.Unlock()
			if !slices.Equal(calls, tc.want) {
				t.Fatalf("reconciles = %v, want %v", calls, tc.want)
			}
			// The run released its account: the next wake starts one.
			ledger.mu.Lock()
			ledger.conc["w"] = 0
			ledger.mu.Unlock()
			if !tr.WakeWorkerPool(ctx, "w") {
				t.Fatal("a wake after the account's run returned started no run")
			}
			tr.waitWorkerPoolRuns()
		})
	}
}

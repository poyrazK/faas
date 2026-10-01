package targets

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type scopedWorkerPoolFake struct {
	*workerPoolFakeEngine
	calls []string
	err   error
}

func (e *scopedWorkerPoolFake) ReconcileWorkerPools(_ context.Context, appID, trigger string) error {
	e.calls = append(e.calls, appID+":"+trigger)
	return e.err
}

func TestTrigger_ReconcilesScopedWorkerPools(t *testing.T) {
	store := &fakeStore{apps: []state.App{{ID: "scoped-worker", WorkloadClass: state.WorkloadClassWorker,
		MaxConcurrency: 5, ScalingPolicy: &state.ScalingPolicy{Target: &state.ScalingTarget{Metric: "queue_depth", Value: 10}}}}}
	engine := &scopedWorkerPoolFake{workerPoolFakeEngine: &workerPoolFakeEngine{fakeEngine: &fakeEngine{}}}
	queue := &fakeQueueStats{byApp: map[string]state.QueueStats{"scoped-worker": {Depth: 1000}}}
	tr := New(store, nil, engine, &fakeLedger{conc: map[string]int{"scoped-worker": 5}}, Options{QueueStatsReader: queue})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(engine.calls) != 1 || engine.calls[0] != "scoped-worker:worker.pool" || len(engine.desired) != 0 || len(engine.admitCalls) != 0 {
		t.Fatalf("scoped=%v legacy=%v request=%v", engine.calls, engine.desired, engine.admitCalls)
	}
	// Scoped reads belong to the scheduler, so a failed aggregate reader cannot
	// turn this tick into request admission or a fabricated empty target.
	engine.err = errors.New("scoped queue unavailable")
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(engine.calls) != 2 || len(engine.desired) != 0 || len(engine.admitCalls) != 0 {
		t.Fatalf("failed scoped reconciliation bypassed backoff: %v", engine.calls)
	}
}

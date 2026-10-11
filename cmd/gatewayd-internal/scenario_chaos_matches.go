package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/chaos"
	"github.com/onebox-faas/faas/pkg/state"
)

type scenarioChaosMatchKey struct {
	runID, callerAppID, generation, ruleID string
}

// scenarioChaosMatchRecorder keeps per-event persistence off the request
// path. The map aggregates repeated HTTP matches and remains bounded by the
// number of active runs, callers, and rules rather than traffic volume.
type scenarioChaosMatchRecorder struct {
	store state.Store
	log   *slog.Logger

	mu      sync.Mutex
	pending map[scenarioChaosMatchKey]int64
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newScenarioChaosMatchRecorder(ctx context.Context, store state.Store, log *slog.Logger) *scenarioChaosMatchRecorder {
	if log == nil {
		log = slog.Default()
	}
	recorder := &scenarioChaosMatchRecorder{
		store: store, log: log, pending: make(map[scenarioChaosMatchKey]int64),
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	go recorder.run(ctx)
	return recorder
}

func (r *scenarioChaosMatchRecorder) Observe(runID, callerAppID, generation string, rule chaos.Rule) {
	if r == nil || runID == "" || callerAppID == "" || generation == "" {
		return
	}
	key := scenarioChaosMatchKey{
		runID: runID, callerAppID: callerAppID, generation: generation, ruleID: chaos.RuleID(rule),
	}
	r.mu.Lock()
	r.pending[key]++
	r.mu.Unlock()
}

func (r *scenarioChaosMatchRecorder) run(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	defer close(r.done)
	for {
		select {
		case <-ticker.C:
			r.flush(ctx)
		case <-ctx.Done():
			r.flush(context.WithoutCancel(ctx))
			return
		case <-r.stop:
			r.flush(context.WithoutCancel(ctx))
			return
		}
	}
}

func (r *scenarioChaosMatchRecorder) flush(ctx context.Context) {
	r.mu.Lock()
	if len(r.pending) == 0 {
		r.mu.Unlock()
		return
	}
	pending := r.pending
	r.pending = make(map[scenarioChaosMatchKey]int64)
	r.mu.Unlock()

	batch := make([]chaos.InjectionBatch, 0, len(pending))
	for key, count := range pending {
		batch = append(batch, chaos.InjectionBatch{
			RunID: key.runID, CallerApp: key.callerAppID, Generation: key.generation,
			RuleID: key.ruleID, Count: count,
		})
	}
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	err := r.store.RecordScenarioTestChaosInjections(writeCtx, batch)
	cancel()
	if err == nil {
		return
	}
	r.mu.Lock()
	for key, count := range pending {
		r.pending[key] += count
	}
	r.mu.Unlock()
	r.log.Warn("gateway: persist scenario chaos matches", "err", err, "keys", len(batch))
}

func (r *scenarioChaosMatchRecorder) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() { close(r.stop) })
	<-r.done
}

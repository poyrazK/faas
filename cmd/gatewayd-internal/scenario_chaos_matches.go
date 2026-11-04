package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
	"github.com/onebox-faas/faas/pkg/state"
)

type scenarioChaosMatchKey struct {
	runID, callerAppID, generation, ruleID string
}

// scenarioChaosMatchWriter persists match counters. Each call is atomic: it
// either records the whole batch or nothing.
type scenarioChaosMatchWriter interface {
	RecordScenarioTestChaosInjections(context.Context, []chaos.InjectionBatch) error
}

// scenarioChaosMatchRecorder keeps per-event persistence off the request
// path. The map aggregates repeated HTTP matches and remains bounded by the
// number of active runs, callers, and rules rather than traffic volume. Guests
// choose request correlation IDs, so the node also caps the map at
// api.ScenarioChaosMatchMaxPendingKeys; matches for new counters past the cap
// are dropped and logged.
type scenarioChaosMatchRecorder struct {
	store scenarioChaosMatchWriter
	log   *slog.Logger

	mu      sync.Mutex
	pending map[scenarioChaosMatchKey]int64
	dropped int64
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newScenarioChaosMatchRecorder(ctx context.Context, store scenarioChaosMatchWriter, log *slog.Logger) *scenarioChaosMatchRecorder {
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
	r.observeID(runID, callerAppID, generation, chaos.RuleID(rule))
}

func (r *scenarioChaosMatchRecorder) ObserveTCPAttempt(runID, callerAppID, generation string, rule chaos.Rule) {
	r.observeID(runID, callerAppID, generation, chaos.TCPAttemptID(rule))
}

func (r *scenarioChaosMatchRecorder) ObserveRequestAttempt(runID, callerAppID, generation string, rule chaos.Rule, requestID string) {
	if rule.CorrelationScope == "" {
		return
	}
	r.observeID(runID, callerAppID, generation, chaos.CorrelationSupportID(rule))
	slot, ok := chaos.RequestCorrelationSlot(runID, rule, requestID)
	if !ok {
		slot = 0
	}
	r.observeID(runID, callerAppID, generation, chaos.CorrelationCounterID(rule, slot))
}

func (r *scenarioChaosMatchRecorder) observeID(runID, callerAppID, generation, ruleID string) {
	if r == nil || runID == "" || callerAppID == "" || generation == "" {
		return
	}
	key := scenarioChaosMatchKey{
		runID: runID, callerAppID: callerAppID, generation: generation, ruleID: ruleID,
	}
	r.mu.Lock()
	r.addLocked(key, 1)
	r.mu.Unlock()
}

// addLocked counts into an existing counter, or into a new one while the node
// is under its pending-counter cap.
func (r *scenarioChaosMatchRecorder) addLocked(key scenarioChaosMatchKey, count int64) {
	if _, ok := r.pending[key]; !ok && len(r.pending) >= api.ScenarioChaosMatchMaxPendingKeys {
		r.dropped += count
		return
	}
	r.pending[key] += count
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

// flush writes pending counters in bounded atomic chunks. A failed chunk and
// the chunks after it return to pending; committed chunks never do, so a retry
// cannot count a match twice.
func (r *scenarioChaosMatchRecorder) flush(ctx context.Context) {
	r.mu.Lock()
	pending, dropped := r.pending, r.dropped
	if len(pending) == 0 && dropped == 0 {
		r.mu.Unlock()
		return
	}
	r.pending = make(map[scenarioChaosMatchKey]int64)
	r.dropped = 0
	r.mu.Unlock()
	if dropped > 0 {
		r.log.Warn("gateway: scenario chaos matches dropped over the node counter cap",
			"matches", dropped, "cap", api.ScenarioChaosMatchMaxPendingKeys)
	}

	batch := make([]chaos.InjectionBatch, 0, len(pending))
	for key, count := range pending {
		batch = append(batch, chaos.InjectionBatch{
			RunID: key.runID, CallerApp: key.callerAppID, Generation: key.generation,
			RuleID: key.ruleID, Count: count,
		})
	}
	for start := 0; start < len(batch); start += api.ScenarioChaosMatchWriteBatch {
		chunk := batch[start:min(start+api.ScenarioChaosMatchWriteBatch, len(batch))]
		writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := r.store.RecordScenarioTestChaosInjections(writeCtx, chunk)
		cancel()
		if err == nil {
			continue
		}
		if errors.Is(err, state.ErrConflict) {
			// An invalid counter can never persist; retrying it would hold
			// back every later chunk.
			r.log.Error("gateway: discard invalid scenario chaos matches", "err", err, "keys", len(chunk))
			continue
		}
		r.requeue(batch[start:])
		r.log.Warn("gateway: persist scenario chaos matches", "err", err, "keys", len(batch)-start)
		return
	}
}

func (r *scenarioChaosMatchRecorder) requeue(batch []chaos.InjectionBatch) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, injection := range batch {
		r.addLocked(scenarioChaosMatchKey{
			runID: injection.RunID, callerAppID: injection.CallerApp,
			generation: injection.Generation, ruleID: injection.RuleID,
		}, injection.Count)
	}
}

func (r *scenarioChaosMatchRecorder) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() { close(r.stop) })
	<-r.done
}

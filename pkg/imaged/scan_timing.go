package imaged

import (
	"context"
	"sync"
	"time"
)

// phaseTimer records how long each phase of an imaged step took. On
// production-us the per-deploy scan took p50 48 s and p90 104 s, while the
// same work on an idle host took about 10 s, and imaged logged a single line
// for the whole step (Hunt 14 H14-12). A timer rides the context so callees,
// including the injected grype runner, can mark their own phases.
type phaseTimer struct {
	mu     sync.Mutex
	start  time.Time
	last   time.Time
	phases []any
}

// Timeline keys: the per-deploy scan, and image preparation around it.
type (
	scanTimingKey        struct{}
	preparationTimingKey struct{}
)

func withPhaseTimer(ctx context.Context, key any) (context.Context, *phaseTimer) {
	now := time.Now()
	t := &phaseTimer{start: now, last: now}
	return context.WithValue(ctx, key, t), t
}

func markPhase(ctx context.Context, key any, name string) {
	t, _ := ctx.Value(key).(*phaseTimer)
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.phases = append(t.phases, name+"_ms", now.Sub(t.last).Milliseconds())
	t.last = now
}

// withScanTiming starts the per-deploy scan timeline.
func withScanTiming(ctx context.Context) (context.Context, *phaseTimer) {
	return withPhaseTimer(ctx, scanTimingKey{})
}

// markScanPhase closes the current scan phase under name; a no-op without a
// scan timeline.
func markScanPhase(ctx context.Context, name string) {
	markPhase(ctx, scanTimingKey{}, name)
}

// markPreparationPhase closes the current image preparation phase under
// name; a no-op without a preparation timeline.
func markPreparationPhase(ctx context.Context, name string) {
	markPhase(ctx, preparationTimingKey{}, name)
}

// attrs returns the recorded phases and the total, as slog key/value pairs.
func (t *phaseTimer) attrs() []any {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]any(nil), t.phases...)
	return append(out, "total_ms", time.Since(t.start).Milliseconds())
}

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

// Timeline keys: the per-deploy scan, image preparation around it, and the
// app layer step inside preparation.
type (
	scanTimingKey        struct{}
	preparationTimingKey struct{}
	layerTimingKey       struct{}
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

// withLayerTiming starts the app layer timeline. The layer step was about
// 27 s of each deploy's preparation at p50 in production (2026-11-17) and
// logged only its total.
func withLayerTiming(ctx context.Context) (context.Context, *phaseTimer) {
	return withPhaseTimer(ctx, layerTimingKey{})
}

// markLayerPhase closes the current app layer phase under name; a no-op
// without a layer timeline.
func markLayerPhase(ctx context.Context, name string) {
	markPhase(ctx, layerTimingKey{}, name)
}

// phaseAttrs returns the recorded phases without the total.
func (t *phaseTimer) phaseAttrs() []any {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]any(nil), t.phases...)
}

// attrs returns the recorded phases and the total, as slog key/value pairs.
func (t *phaseTimer) attrs() []any {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]any(nil), t.phases...)
	return append(out, "total_ms", time.Since(t.start).Milliseconds())
}

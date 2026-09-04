package gateway

import (
	"context"
	"sync"
	"sync/atomic"
)

// staleTargetSignal is request-local state shared by the handler and the
// forwarding bridge. A bridge failure can mean that the routing cache still
// points at an instance whose vmmd/netns has disappeared; keeping that target
// cached turns one infrastructure failure into a persistent 502 loop. The
// signal deliberately lives in context rather than in an HTTP header so the
// internal decision can never leak to a customer response.
type staleTargetSignal struct {
	stale   atomic.Bool
	claimed sync.Once
	onStale func()
}

type staleTargetSignalKey struct{}

func withStaleTargetSignal(ctx context.Context, signal *staleTargetSignal) context.Context {
	return context.WithValue(ctx, staleTargetSignalKey{}, signal)
}

func markStaleTarget(ctx context.Context) {
	if signal, ok := ctx.Value(staleTargetSignalKey{}).(*staleTargetSignal); ok && signal != nil {
		signal.stale.Store(true)
		// Quarantine the target as soon as the transport proves it is
		// stale. The handler used to wait until the forwarder returned,
		// which allowed concurrent requests that had not picked yet to
		// select the same dead instance and created a 503 storm. Once is
		// important here: a streaming forwarder can observe more than
		// one transport failure while its request is being torn down.
		signal.claimed.Do(func() {
			if signal.onStale != nil {
				signal.onStale()
			}
		})
	}
}

func staleTargetDetected(ctx context.Context) bool {
	signal, ok := ctx.Value(staleTargetSignalKey{}).(*staleTargetSignal)
	return ok && signal != nil && signal.stale.Load()
}

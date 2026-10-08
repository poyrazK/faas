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

// staleTargetMarked reports whether the attempt carried by ctx proved its
// target dead at the transport layer.
func staleTargetMarked(ctx context.Context) bool {
	signal, ok := ctx.Value(staleTargetSignalKey{}).(*staleTargetSignal)
	return ok && signal != nil && signal.stale.Load()
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

// A delayed failure from an old forward cannot retire a replacement target.
func evictStaleTarget(backend any, appID string, target Target) bool {
	target.AppID = appID
	if scoped, ok := backend.(interface{ EvictRoutedTarget(Target) }); ok {
		scoped.EvictRoutedTarget(target)
		return true
	}
	if legacy, ok := backend.(interface{ EvictInstance(string, string) }); ok {
		legacy.EvictInstance(appID, target.InstanceID)
		return true
	}
	return false
}

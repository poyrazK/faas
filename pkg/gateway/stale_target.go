package gateway

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
)

// staleTargetSignal is request-local state shared by the handler and the
// forwarding bridge. A bridge failure can mean that the routing cache still
// points at an instance whose vmmd/netns has disappeared; keeping that target
// cached turns one infrastructure failure into a persistent 502 loop. The
// signal deliberately lives in context rather than in an HTTP header so the
// internal decision can never leak to a customer response.
type staleTargetSignal struct {
	stale atomic.Bool
	// frozen marks a target vmmd refused because its guest is frozen
	// (ADR-1151): idle, not dead, and thawed by schedd under the same ID.
	frozen atomic.Bool
	// undelivered is set with stale when the bridge proved the request never
	// reached any guest (ADR-1133), so a re-route is safe for any method.
	undelivered atomic.Bool
	claimed     sync.Once
	onStale     func()
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

// markStaleTargetErr marks the attempt's target stale from a forward error.
// When vmmd refused the forward because the guest is frozen, the target is
// flagged frozen first so the handler withdraws it without quarantine.
func markStaleTargetErr(ctx context.Context, err error) {
	if signal, ok := ctx.Value(staleTargetSignalKey{}).(*staleTargetSignal); ok && signal != nil {
		if instanceFrozenRefusal(err) {
			signal.frozen.Store(true)
		}
	}
	markStaleTarget(ctx)
}

// markUndeliveredTarget is markStaleTarget for failures that prove the request
// never reached any guest: no client for the target's node, the forward stream
// could not be opened, or vmmd answered NotFound for the instance before any
// response (it checks the instance before dialing the guest bridge).
func markUndeliveredTarget(ctx context.Context) {
	markUndeliveredTargetErr(ctx, nil)
}

// markUndeliveredTargetErr is markUndeliveredTarget for a forward error, which
// can also show that the guest is frozen (ADR-1151).
func markUndeliveredTargetErr(ctx context.Context, err error) {
	if signal, ok := ctx.Value(staleTargetSignalKey{}).(*staleTargetSignal); ok && signal != nil {
		signal.undelivered.Store(true)
	}
	markStaleTargetErr(ctx, err)
}

// instanceFrozenRefusal reports whether err is vmmd refusing a forward
// because the guest is frozen (ADR-1151). vmmd refuses before it dials the
// guest, so the guest never saw the request.
func instanceFrozenRefusal(err error) bool {
	if err == nil {
		return false
	}
	p, ok := grpcerr.FromStatus(err)
	return ok && p != nil && p.Code == api.CodeInstanceFrozen
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

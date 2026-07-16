package gateway

import (
	"context"
	"errors"
	"sync"
	"time"
)

// WakeGate holds requests during a cold wake (spec §4.1 wake-blocking). When an
// app has no running instance, many simultaneous requests must trigger exactly
// ONE wake and all wait on it (single-flight per app), up to a cap; past the cap,
// or past the TTL, the caller returns 503 + Retry-After. This coalescing is what
// makes a burst to a parked app cost one restore, not N.
type WakeGate struct {
	mu       sync.Mutex
	inflight map[string]*wakeCall
	cap      int
	ttl      time.Duration
}

type wakeCall struct {
	done    chan struct{}
	err     error
	waiters int
}

// ErrQueueFull is returned when the per-app waiter cap is exceeded (→ 503).
var ErrQueueFull = errors.New("gateway: wake queue full")

// NewWakeGate returns a gate with the given per-app waiter cap and wake TTL
// (spec §4.1: 512 / 30 s).
func NewWakeGate(capacity int, ttl time.Duration) *WakeGate {
	if capacity < 1 {
		capacity = 1
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &WakeGate{inflight: map[string]*wakeCall{}, cap: capacity, ttl: ttl}
}

// Wait ensures an instance for appID is awake, coalescing concurrent callers onto
// a single ensure() invocation. The leader runs ensure under the gate's TTL in a
// detached goroutine (so the triggering request cancelling doesn't abort the wake
// for other waiters); followers wait on the same result. Returns ErrQueueFull if
// the waiter cap is exceeded, the ensure error, or the caller's ctx error.
func (g *WakeGate) Wait(ctx context.Context, appID string, ensure func(context.Context) error) error {
	g.mu.Lock()
	if call, ok := g.inflight[appID]; ok {
		if call.waiters >= g.cap {
			g.mu.Unlock()
			return ErrQueueFull
		}
		call.waiters++
		g.mu.Unlock()
		return g.await(ctx, call)
	}

	call := &wakeCall{done: make(chan struct{}), waiters: 1}
	g.inflight[appID] = call
	g.mu.Unlock()

	go func() {
		ectx, cancel := context.WithTimeout(context.Background(), g.ttl)
		defer cancel()
		call.err = ensure(ectx)
		g.mu.Lock()
		delete(g.inflight, appID)
		g.mu.Unlock()
		close(call.done)
	}()

	return g.await(ctx, call)
}

// InflightWaiters returns the current waiter count for appID (0 if none). For
// the gateway_queue_depth metric and tests.
func (g *WakeGate) InflightWaiters(appID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if call, ok := g.inflight[appID]; ok {
		return call.waiters
	}
	return 0
}

func (g *WakeGate) await(ctx context.Context, call *wakeCall) error {
	select {
	case <-call.done:
		return call.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

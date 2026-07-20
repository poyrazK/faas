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
	// onChange is called whenever an entry's waiter count or completion state
	// changes, so the metrics layer can keep gateway_queue_depth current.
	// Optional; nil-safe at every call site.
	onChange func(appID string, depth int)
	// metrics observes how long each caller waited in the queue. Optional;
	// nil keeps the gate usable in unit tests that don't wire metrics.
	metrics *Metrics
}

// SetMetrics attaches a *Metrics so Wait can observe the per-caller wait
// duration (gateway_wake_queue_wait_seconds). Safe to call before serve
// starts; nil-safe on the gate's read path.
func (g *WakeGate) SetMetrics(m *Metrics) { g.metrics = m }

type wakeCall struct {
	done      chan struct{}
	err       error
	waiters   int
	completed bool // ensure() has returned; entry stays parked until drain
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
//
// shouldWake is consulted by the leader under the gate lock *after* becoming
// leader and *before* dispatching ensure. If shouldWake returns false (the app
// already has a ready instance via a peer's wake, observable via the Backend's
// Target), the leader short-circuits with err=nil and no ensure() call runs.
// This closes the race where a goroutine reaches Wait after the previous wake
// has set the instance running but before its old Target read sees it.
//
// A completed but un-drained entry stays in the map until the last follower
// departs, so a follow-on request that arrives microseconds after ensure()
// returns cannot trigger a second wake (regression test:
// TestConcurrentColdRequestsCoalesceToOneWake).
//
// Queue-wait timing: we observe time.Since(start) for callers that
// actually waited (joined an in-flight call, or were the leader and
// dispatched ensure). ErrQueueFull and ctx.Err() returns are NOT
// recorded as queue-wait — they aren't wait, they're rejection or
// cancellation, and recording them as ~0ms observations would push
// the histogram's p95 toward zero during overload (the very signal
// the SLO dashboard needs to surface).
func (g *WakeGate) Wait(
	ctx context.Context,
	appID string,
	shouldWake func() bool,
	ensure func(context.Context) error,
) error {
	start := time.Now()
	// observed is set true only on the paths where the caller actually
	// spent time in the queue. The deferred observer checks this flag.
	observed := false
	defer func() {
		if observed && g.metrics != nil {
			g.metrics.ObserveWakeQueueWait(time.Since(start))
		}
	}()

	g.mu.Lock()
	if call, ok := g.inflight[appID]; ok {
		if call.waiters >= g.cap {
			depth := call.waiters
			g.mu.Unlock()
			if g.onChange != nil {
				g.onChange(appID, depth)
			}
			return ErrQueueFull
		}
		call.waiters++
		depth := call.waiters
		g.mu.Unlock()
		if g.onChange != nil {
			g.onChange(appID, depth)
		}
		observed = true
		// Hold the followers' reference until await returns; release on exit.
		err := g.await(ctx, call)
		g.release(appID, call)
		// ctx.Err() is also "didn't wait for an ensure result" — skip
		// the metric on cancellation so a hung client's cancellation
		// doesn't pollute the wake-latency histogram.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			observed = false
		}
		return err
	}

	call := &wakeCall{done: make(chan struct{}), waiters: 1}
	g.inflight[appID] = call
	g.mu.Unlock()
	if g.onChange != nil {
		g.onChange(appID, 1)
	}

	// Leader-only: skip the wake if the Backend already has a ready instance
	// (a peer's wake just finished and we observe it here). shouldWake runs
	// synchronously under no lock; the Backend must serialize state itself.
	if !shouldWake() {
		g.mu.Lock()
		call.completed = true
		g.mu.Unlock()
		close(call.done)
		// Treat the leader itself as a waiter so release() can drop the
		// entry; a follower that arrives later increments waiters to 2 then
		// drops to 1 after await; both leader and follower call release().
		_ = g.await(ctx, call)
		g.release(appID, call)
		return nil
	}

	//nolint:contextcheck // leader goroutine deliberately detaches from the
	// caller's ctx via context.Background() — the wake must outlive the
	// triggering request so other queued waiters get the same instance.
	// This is the load-bearing single-flight coalescing invariant (spec §4.1).
	go func() {
		ectx, cancel := context.WithTimeout(context.Background(), g.ttl)
		defer cancel()
		call.err = ensure(ectx)
		g.mu.Lock()
		call.completed = true
		g.mu.Unlock()
		close(call.done)
	}()

	observed = true
	err := g.await(ctx, call)
	g.release(appID, call)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		observed = false
	}
	return err
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

// release decrements the waiter count and, if the leader's ensure has finished
// and no other follower is still waiting, removes the entry. Done on the
// leader's path AND every follower's path so a completed wake is observable to
// every concurrent caller regardless of arrival order.
func (g *WakeGate) release(appID string, call *wakeCall) {
	g.mu.Lock()
	defer g.mu.Unlock()
	call.waiters--
	if call.completed && call.waiters == 0 {
		delete(g.inflight, appID)
	}
	if g.onChange != nil {
		depth := 0
		if c, ok := g.inflight[appID]; ok {
			depth = c.waiters
		}
		g.onChange(appID, depth)
	}
}

func (g *WakeGate) await(ctx context.Context, call *wakeCall) error {
	select {
	case <-call.done:
		return call.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

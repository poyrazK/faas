// Package drain owns the WaitGroup-backed per-request in-flight tracker
// the gateway daemons use to bound shutdown by known in-flight request
// goroutines, not by a hard wall-clock deadline (issue #587 / PR-A).
//
// # Why a separate package
//
// The shipped ConnStateTracker (pkg/gateway/inflight.go) counts
// *connections*, not *requests*. A single net/http connection can
// pipeline many requests; a hijacked raw-stream Upgrade tail runs a
// pump goroutine entirely outside any ServeHTTP envelope. Neither is
// observable via http.Server.ConnState transitions alone, and a drain
// built on top of ConnStateTracker would close the listener while
// pump goroutines and pipelined requests are still writing.
//
// Drain.Tracker is the per-request (and per-upgrade-pump) counterpart.
// Each request handler does:
//
//	defer drain.Tracker.Begin("http")()
//
// The returned closure is the Done side of the WaitGroup. Symmetric
// Begin/Done is the policy (per the cluster plan's "Decisions baked
// in" §1): the listener's goroutine envelope is bounded, not
// arbitrary downstream pumps. Cancelled requests still drain because
// their request goroutine still exits — downstream pumps have their
// own TTLs (the WakeGate leader's 30s ceiling, the raw-stream
// pump's half-close on r.Context().Done(), etc.).
//
// Why drain does NOT own *http.Server
//
// The cmd's runDrain / shutdown select already owns the
// second-SIGTERM-cancellable grace dance and the listener's Serve
// error channel. Folding that policy into a package would either
// duplicate it or move signal-handling into drain (wrong layer).
// The contract is: cmd calls Drain(ctx, deadline) AFTER it has
// stopped accepting new connections, BEFORE it closes the listeners.
//
// Drain is a helper, not a server wrapper. Future PRs (the #607
// reverse-proxy epic) can wrap the Tracker in a per-(app, plan)
// counter without changing this surface.
//
// # Thread-safety
//
// All exported methods are safe for concurrent use. Begin and Done
// (the closure) are the only writer pair; Inflight is an atomic load.
// Drain uses a separate goroutine that selects on ctx.Done and the
// WaitGroup, so the caller never blocks past the deadline or ctx.
package drain

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// DrainGrace is the default shutdown grace budget. The daemon's
// gateway uses it inside a shared server-shutdown budget, leaving time
// for telemetry flush and process teardown. Override at the call site by
// passing a different deadline to Drain.
const DrainGrace = 25 * time.Second

// DrainOutcome is the labelled result of a Drain call. Surfaced as
// the `outcome` label of the gateway_drain_wait_seconds histogram so
// operators can distinguish a clean shutdown from a forced one.
type DrainOutcome string

const (
	// OutcomeClean: ctx was not cancelled and the WaitGroup drained
	// before the deadline. systemd's Restart=on-failure does NOT
	// trigger on the resulting exit-0.
	OutcomeClean DrainOutcome = "clean"
	// OutcomeDeadlineExceeded: the WaitGroup did not drain within
	// the deadline. Some in-flight requests were force-cut. The
	// caller should return a non-nil error so systemd restarts.
	OutcomeDeadlineExceeded DrainOutcome = "deadline_exceeded"
	// OutcomeCancelled: ctx was cancelled (typically second SIGTERM
	// in runDrain) before the WaitGroup drained. Same semantics as
	// deadline_exceeded: caller returns ctx.Err() so systemd restarts.
	OutcomeCancelled DrainOutcome = "ctx_cancelled"
)

// Tracker counts in-flight request goroutines and waits for them to
// exit on Drain. Constructed once per daemon (cmd/gatewayd-public and
// cmd/gatewayd-internal each own one) and shared across every
// ServeHTTP path via setters on Handler / InternalReverseProxy /
// TraceHandler / ControlMux.
type Tracker struct {
	// inflight is the live counter. Atomic for cheap Inflight()
	// reads from the Prometheus gauge goroutine.
	inflight atomic.Int64
	// addMu serialises Begin's draining-check + wg.Add pair
	// against Drain's draining-set + wg.Wait pair. The race
	// detector forbids positive WaitGroup.Add after Wait has
	// started (Go docs: "calls with a positive delta that occur
	// when the counter is zero must happen before a Wait"). The
	// mutex makes the check + Add atomic with respect to
	// Drain's set + Wait, so a Begin that races Drain either
	// completes its Add before Drain's Wait starts (and is
	// properly awaited) or observes draining==true and returns
	// a no-op closure.
	addMu sync.Mutex
	// draining is set true under addMu by Drain; Begin checks it
	// under addMu. The mutex is what makes the check-then-act
	// race-free, not the atomic itself.
	draining bool
	// wg is the drain barrier. Held inside Drain until either all
	// Begin closures have fired Done or the deadline fires.
	wg sync.WaitGroup
	// maxInflight records the high-water mark since construction.
	// Diagnostic only; not exported.
	maxInflight atomic.Int64
}

// NewTracker constructs a zero-valued Tracker. The zero value is
// NOT directly usable (sync.WaitGroup must not be copied after first
// use); always go through NewTracker.
func NewTracker() *Tracker {
	return &Tracker{}
}

// Begin increments the in-flight counter and returns the Done
// closure. The label is purely for the {op} Prometheus label and
// for log correlation — it is not interpreted by the drain logic.
//
// Usage:
//
//	defer drain.Tracker.Begin("http")()
//
// The deferred call fires on every return path of the enclosing
// function, including panics that recover up the stack. Begin/Done
// symmetry is the contract; the Prometheus gauge is only accurate
// if every Begin has exactly one matching Done.
//
// For long-lived goroutines that outlive the request envelope (the
// raw-stream Upgrade pump at pkg/gateway/forwardproxy.go:830), call
// Begin directly and stash the Done closure in conn-scoped state so
// it fires when the pump exits.
//
// Once Drain has started, Begin returns a no-op closure. The
// in-flight counter is NOT incremented and the WaitGroup is NOT
// touched. This avoids the sync.WaitGroup "positive delta after
// Wait" footgun (Go docs: "calls with a positive delta that occur
// when the counter is zero must happen before a Wait"). The
// invariant at the call site is that Drain only fires AFTER
// srv.Shutdown has stopped accepting new connections, so any
// goroutine that observes draining==true is either a stray from
// a pipeline race or a pump goroutine the daemon chose not to
// track — either way it must not block the drain.
func (t *Tracker) Begin(label string) func() {
	t.addMu.Lock()
	if t.draining {
		t.addMu.Unlock()
		return func() {} // no-op once drain has started
	}
	t.inflight.Add(1)
	// bump the HWM; non-atomic with the Add but that's fine — the
	// HWM is a diagnostic, not a load-bearing counter.
	for {
		cur := t.maxInflight.Load()
		now := t.inflight.Load()
		if now <= cur {
			break
		}
		if t.maxInflight.CompareAndSwap(cur, now) {
			break
		}
	}
	t.wg.Add(1)
	t.addMu.Unlock()
	return func() {
		t.inflight.Add(-1)
		t.wg.Done()
	}
}

// Inflight returns the current number of in-flight Begin calls
// without matching Done calls. Safe for concurrent use; intended for
// the gateway_inflight_requests Prometheus gauge.
func (t *Tracker) Inflight() int64 {
	return t.inflight.Load()
}

// MaxInflight returns the highest in-flight count observed since
// construction. Diagnostic; surfaced only when the caller asks for
// it (e.g. on a non-clean Drain outcome).
func (t *Tracker) MaxInflight() int64 {
	return t.maxInflight.Load()
}

// Drain blocks until either every outstanding Begin has fired Done
// or ctx is cancelled or the deadline elapses, whichever comes
// first. Returns the outcome label so the caller can distinguish a
// clean shutdown (return nil; systemd does NOT restart) from a
// forced one (return the ctx error; systemd's Restart=on-failure
// triggers).
//
// Concurrency: Drain is safe to call once. Calling it a second time
// is a programmer error; the underlying sync.WaitGroup has undefined
// behaviour on concurrent Wait. The daemons call Drain exactly
// once on shutdown.
//
// Pre-condition: the caller must have already stopped accepting new
// connections (srv.Shutdown or close(listener)) so no new Begin
// calls can race the deadline. Drain itself does NOT close the
// listener — that's the cmd's job. The contract is "wait, then
// the caller closes."
//
// Side effect: sets t.draining=true so any subsequent Begin call
// becomes a no-op. This is the late-Begin guard that makes the
// drain safe against pipeline races between srv.Shutdown returning
// and the last in-flight request goroutine firing Done.
//
// nolint:contextcheck // Drain's contract IS to receive the caller-
// supplied ctx and respect its cancellation + deadline (the
// shutdownCtx the daemon passes already has a 25s timeout derived
// from `DrainGrace`). Wrapping ctx in context.WithTimeout here
// would silently mask a forced-Shutdown by extending the drain
// past the daemon's intended budget; lint's heuristic doesn't
// apply because the `deadline` parameter is a separate knob
// from ctx, not a redundant timeout-on-ctx.
func (t *Tracker) Drain(ctx context.Context, deadline time.Duration) (DrainOutcome, error) {
	if deadline <= 0 {
		deadline = DrainGrace
	}
	if ctx == nil {
		ctx = context.Background()
	}
	t.addMu.Lock()
	t.draining = true
	// Capture the current inflight count while holding the mutex
	// so we know whether we have anything to wait for. We can't
	// simply check after releasing the mutex because new Begins
	// are now refused, so inflight is stable from here on.
	pending := t.inflight.Load()
	t.addMu.Unlock()

	// Fast-path: nothing in flight, nothing to wait for.
	if pending == 0 {
		return OutcomeClean, nil
	}

	// Compose a sub-context that fires on either the caller-cancel
	// OR the deadline. We pick the smaller of (ctx.Done) and
	// (deadline from now).
	dctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	done := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return OutcomeClean, nil
	case <-dctx.Done():
		// Distinguish ctx-cancel from deadline-exceeded by
		// checking which fired first. If the caller's ctx was
		// already cancelled before we entered, ctx.Err() is
		// non-nil regardless of the deadline.
		if ctx.Err() != nil {
			return OutcomeCancelled, ctx.Err()
		}
		if errors.Is(dctx.Err(), context.DeadlineExceeded) {
			return OutcomeDeadlineExceeded, dctx.Err()
		}
		// Defensive fallback: dctx is cancelled but we can't tell
		// which arm fired. Treat as deadline (safer to surface
		// the worst case).
		return OutcomeDeadlineExceeded, dctx.Err()
	}
}

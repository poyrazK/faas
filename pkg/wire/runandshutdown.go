// Package wire — runandshutdown.go owns the drain helper
// (issue #571 PR-A2, commit 11). The helper generalises the
// drain-then-exit pattern cmd/gatewayd-public/drain.go uses
// today (pgProbeSig.Set(false, "draining") on ctx.Done) so
// every cmd/<daemon>/main.go gets the same behaviour with a
// single helper call.
//
// Why one helper: every daemon with a /readyz endpoint needs
// the SAME drain semantic — when SIGTERM lands, /readyz must
// flip 200 → 503 BEFORE the process exits, so the LB stops
// routing new traffic and the operator dashboard reflects
// the draining state. Implementing this per daemon (5+
// daemons in this PR-A2 alone, plus every future daemon)
// means N copies of the same ctx.Done + Set(false, "draining")
// + MarkReady(false, "draining") snippet. One helper makes the
// drain shape auditable in one place.
//
// Why a separate file: the drain helper is logically
// orthogonal to the probe-fan-in shape in readiness.go. The
// probe says "are my dependencies healthy?"; the drain helper
// says "I'm about to leave, flip everything false." Keeping
// them in separate files makes the contract surfaces obvious
// and avoids a 400-line readiness.go.
package wire

import (
	"context"
	"log/slog"
)

// RunAndShutdown wires a daemon's run function into a
// ctx.Done-triggered drain. It is intended as a thin wrapper
// around `fn(ctx, log)` that adds three guarantees:
//
//  1. Every signal registered on probe is flipped to
//     (false, "draining") the moment ctx is cancelled. This
//     makes /readyz return 503 immediately so the LB stops
//     routing — before fn returns and the process exits.
//  2. The daemon_ready{daemon} gauge is set to 0 (with reason
//     "draining") the same moment. The §12 "Fleet readiness"
//     panel reflects the draining state in real time.
//  3. fn is still allowed to run its cleanup (graceful
//     listener shutdown, draining in-flight requests) — the
//     helper does NOT short-circuit fn. The "draining" flip
//     and the gauge reset happen in parallel with fn's
//     cleanup path, then RunAndShutdown returns fn's error.
//
// name identifies the daemon in the daemon_ready{daemon} label
// (e.g. "vmmd", "schedd"). probe is the ReadyzProbe the
// daemon already constructed; nil is allowed (no signals to
// flip). fn is the daemon's run function — same signature as
// wire.Daemon(fn) uses internally, so callers can adopt the
// helper without rewriting their entrypoint.
//
// RunAndShutdown is meant to be the LAST step in a daemon's
// runWithDeps: callers wire the probe + log + ctx, register
// signals, then call RunAndShutdown as the final return.
//
// Returns whatever fn returns. The drain transitions are
// idempotent — calling RunAndShutdown twice on the same
// probe is safe (each signal is Set(false, "draining") at
// most once per RunAndShutdown call; the second call sees
// the signals already false and re-sets them harmlessly).
//
// Drain triggers: the watcher goroutine flips signals + the
// gauge on whichever of (ctx.Done, fn-returned) fires first.
// fn-returned matters because fn can finish early on a startup
// error (the daemon never made it to "serving"); we want /readyz
// to flip 503 in that case too so the LB stops routing to a
// daemon that isn't ready. The two triggers are merged into a
// single `drained` channel via a small select in the watcher
// goroutine; the main goroutine reads `<-drained` to ensure the
// drain finished before returning.
func RunAndShutdown(ctx context.Context, log *slog.Logger, probe *ReadyzProbe, name string, fn func(ctx context.Context, log *slog.Logger) error) error {
	if log == nil {
		log = slog.Default()
	}
	drained := make(chan struct{})
	fnReturned := make(chan struct{})
	// drainAndClose flips signals, resets the gauge, and
	// closes `drained`. Called from the watcher goroutine
	// when one of the two triggers fires. The watcher goroutine
	// is the only caller — no need for sync.Once.
	drainAndClose := func() {
		if probe != nil {
			probe.Drain(name, log)
		}
		close(drained)
	}
	// Watcher goroutine: whichever of ctx.Done() or fn-returned
	// fires first triggers the drain. nil probe = no signals
	// to flip, no goroutine needed; we still close `drained`
	// synchronously so the main goroutine's `<-drained` doesn't
	// block forever.
	if probe != nil {
		go func() {
			select {
			case <-ctx.Done():
				drainAndClose()
			case <-fnReturned:
				drainAndClose()
			}
		}()
	} else {
		close(drained)
	}
	err := fn(ctx, log)
	// Tell the watcher (if any) that fn is done so it can
	// trigger the drain even if ctx hasn't been cancelled
	// (the "fn returned early" path).
	close(fnReturned)
	// Wait for the drain to complete before returning. This
	// is the load-bearing invariant — a caller that exits
	// the process immediately after RunAndShutdown returns
	// must not race the gauge reset.
	<-drained
	defaultOps.MarkReady(name, false, "shutdown complete")
	return err
}

// Drain flips every signal registered on probe to
// (false, "draining") and updates the daemon_ready{daemon}
// gauge to 0. Intended to be called from RunAndShutdown's
// watcher goroutine, but exported so a daemon that wants
// bespoke drain semantics (e.g. gatewayd-public's drain
// tracker wrapping) can compose it with its own logic.
//
// Order is load-bearing (Finding 4 from PR #1091 review):
//
//  1. Fire every registered stopper synchronously. Each
//     stopper (NewPGPingSignal, NewStalenessSignal, and
//     per-daemon helpers like vmmdDialSignal /
//     buildsDirSignal / writableSignal / meterd's loop.Health
//     adapter) closes its goroutine's stop channel and
//     blocks until the goroutine exits. Firing stoppers FIRST
//     guarantees no helper is alive to re-flip a signal after
//     we set it.
//  2. Flip every signal to (false, "draining"). With all
//     helpers stopped, the flip is uncontended.
//  3. Reset the daemon_ready{daemon} gauge to 0 (reason
//     "draining") so the §12 "Fleet readiness" panel
//     reflects the draining state in real time.
//
// nil probe and nil log are tolerated (no-op).
func (p *ReadyzProbe) Drain(name string, log *slog.Logger) {
	if p == nil {
		return
	}
	p.mu.RLock()
	signals := make([]*ReadySignal, len(p.signals))
	copy(signals, p.signals)
	stoppers := make([]func(), len(p.stoppers))
	copy(stoppers, p.stoppers)
	p.mu.RUnlock()
	// Step 1: stop helpers BEFORE flipping signals. Each stopper
	// blocks until its helper goroutine exits, so by the time the
	// loop returns no helper can re-flip a signal.
	for _, st := range stoppers {
		st()
	}
	// Step 2: flip signals to (false, "draining") now that all
	// helpers are stopped.
	for _, s := range signals {
		s.Set(false, "draining")
	}
	// Step 3: reset the daemon-level gauge.
	defaultOps.MarkReady(name, false, "draining")
	if log != nil {
		log.Info("readiness drained", "name", name, "signals", len(signals), "stoppers", len(stoppers))
	}
}

// Package main — readiness.go constructs the meterd-side
// /readyz probe (issue #571 PR-A2). meterd already exposes
// /healthz (cmd/meterd/main.go:~1000) which renders a rich JSON
// verdict via loop.Health(time.Now()). This file adds a
// short-ASCII /readyz on the same metrics mux. Readiness is driven by
// loop.Readiness, which gates activation on the core sample and quota loops;
// hourly maintenance loops remain visible through /healthz without forcing a
// clean restart to wait an hour before it can become ready.
//
// The adapter goroutine fires the first evaluation
// synchronously inside the goroutine (before the 1 s ticker
// starts), so the first /readyz scrape after meterd boot
// observes a real loop.Readiness verdict — not a pre-armed
// (true, ""). PR #1091 review Finding 7.
//
// Why an adapter goroutine (instead of calling loop.Readiness on
// the /readyz hot path): the adapter ticks at 1 s
// to keep the ReadySignal up-to-date with the LAST tick fire
// times so a /readyz scrape that lands between two /healthz
// scrapes still sees the current verdict.
//
// When loop.Health reports Healthy==false, the ReadySignal
// reason surfaces the stale tick names comma-separated so an
// operator reading /readyz knows which sweep is hung. The
// /healthz handler still emits the per-tick JSON for
// dashboards.
package main

import (
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/wire"
)

// BuildReadinessProbe constructs the meterd /readyz probe
// driven by loop.Health. The probe starts at (false, "meterd
// loop not yet evaluated") — the first adapter tick (which fires
// synchronously inside the goroutine, before the 1 s ticker)
// evaluates loop.Health and flips to the canonical verdict.
//
// Returns the probe. Drain() (RunAndShutdown's drain path) is
// responsible for firing the helper-goroutine stopper, so the
// caller does not get a stop func — pkg/wire.Drain owns the
// lifecycle of the helper goroutine via RegisterSignal's
// stopper arg.
//
// PR #1091 review Finding 7: the previous shape pre-armed the
// signal to (true, "") at construction, then the first tick
// flipped to false if any sweep was stale. That left a window
// where /readyz reported ready before any real evaluation had
// happened — same invariant violation the wire-side
// NewStalenessSignal pre-arm did. Now the signal starts at
// (false, "meterd loop not yet evaluated") and the first tick
// (which fires synchronously inside the goroutine) is the
// canonical readiness flip.
func BuildReadinessProbe(loop *meter.Loop) *wire.ReadyzProbe {
	sig := wire.NewReadySignalForTest(false, "meterd loop not yet evaluated")

	stop := make(chan struct{})
	done := make(chan struct{})
	stopFn := func() {
		close(stop)
		<-done
		sig.Set(false, "meterd stopping")
	}
	go func() {
		defer close(done)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		// First evaluation fires immediately (the first tick is
		// at +1 s).
		evaluate := func() {
			if loop == nil {
				sig.Set(false, "meterd loop nil")
				return
			}
			status := loop.Readiness(time.Now())
			if status.Healthy {
				sig.Set(true, "")
				return
			}
			if len(status.Stale) == 0 {
				sig.Set(false, "unhealthy")
				return
			}
			sig.Set(false, "stale: "+strings.Join(status.Stale, ","))
		}
		evaluate()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				evaluate()
			}
		}
	}()
	p := &wire.ReadyzProbe{}
	p.RegisterSignal(sig, stopFn)
	return p
}

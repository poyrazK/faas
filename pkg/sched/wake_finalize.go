// adr: 20261105090246 — a booted wake answers after its RUNNING publication;
// the bookkeeping that records what the boot proved follows it.
package sched

import (
	"context"
	"sync"
	"time"
)

// wakeFinalizeTimeout bounds a booted wake's bookkeeping once the wake has
// answered: the observed workload class and the secret delivery record.
const wakeFinalizeTimeout = 10 * time.Second

// WithAsyncWakeFinalize lets a booted wake answer as soon as its RUNNING
// publication commits (schedd `async_wake_finalize`, off by default). The
// bookkeeping after the publication, the observed workload class with its
// audit row and the secret delivery record with its audit row, then runs in
// the background instead of in front of the answer. Each piece was already
// best-effort: a failed write is logged and never fails the wake.
func (e *Engine) WithAsyncWakeFinalize() *Engine {
	if e == nil {
		return e
	}
	e.asyncWakeFinalize.Store(true)
	return e
}

// finalizeBootedWake runs finalize, a published boot's bookkeeping: inline,
// or in the background when the wake answers first. The background run
// detaches from the wake's context, which ends with the answer.
func (e *Engine) finalizeBootedWake(ctx context.Context, finalize func(context.Context)) {
	if !e.asyncWakeFinalize.Load() {
		finalize(ctx)
		return
	}
	e.wakeFinalizers.start()
	go func() {
		defer e.wakeFinalizers.done()
		finalizeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), wakeFinalizeTimeout)
		defer cancel()
		finalize(finalizeCtx)
	}()
}

// WaitWakeFinalize waits until the bookkeeping of every wake that has
// answered finished, or ctx ends. schedd calls it on shutdown so a clean
// stop keeps those records.
func (e *Engine) WaitWakeFinalize(ctx context.Context) error {
	if e == nil {
		return nil
	}
	return e.wakeFinalizers.wait(ctx)
}

// wakeFinalizerSet counts background bookkeeping. Unlike a sync.WaitGroup,
// a wake may start bookkeeping while a shutdown already waits.
type wakeFinalizerSet struct {
	mu      sync.Mutex
	running int
	waiters []chan struct{}
}

func (s *wakeFinalizerSet) start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running++
}

func (s *wakeFinalizerSet) done() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running--
	if s.running > 0 {
		return
	}
	for _, waiter := range s.waiters {
		close(waiter)
	}
	s.waiters = nil
}

func (s *wakeFinalizerSet) wait(ctx context.Context) error {
	s.mu.Lock()
	if s.running == 0 {
		s.mu.Unlock()
		return nil
	}
	idle := make(chan struct{})
	s.waiters = append(s.waiters, idle)
	s.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

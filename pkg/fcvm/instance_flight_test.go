package fcvm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// A primary operation can wake as soon as its Done channel closes. Hold its
// parent's cancellation unregistration so that handoff is deterministic.
type heldFlightCancellationParent struct {
	context.Context
	done, unregistering, release chan struct{}
}

func (p *heldFlightCancellationParent) Done() <-chan struct{} { return p.done }

func (p *heldFlightCancellationParent) AfterFunc(func()) func() bool {
	return func() bool {
		close(p.unregistering)
		<-p.release
		return true
	}
}

// adr: 568 — teardown cancels recovery before waking its operation producer.
func TestInstanceFlightCancelsRecoveryBeforeOperationUnwinds(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	parent := &heldFlightCancellationParent{
		Context: ctx, done: make(chan struct{}),
		unregistering: make(chan struct{}), release: make(chan struct{}),
	}
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(parent.release) }) }
	t.Cleanup(unblock)
	operation, flight := newInstanceFlight(parent)
	finished := make(chan struct{})
	go func() { flight.cancel(); close(finished) }()
	select {
	case <-parent.unregistering:
	case <-ctx.Done():
		t.Fatal("operation cancellation did not reach the handoff")
	}
	select {
	case <-operation.Done():
	default:
		t.Fatal("primary operation was not cancelled at the handoff")
	}
	if err := flight.recoveryCtx.Err(); !errors.Is(err, context.Canceled) {
		t.Errorf("operation can unwind with recovery still available: %v", err)
	}
	unblock()
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("flight cancellation did not finish")
	}
	flight.cancel() // Cancellation remains idempotent after both contexts stop.
}

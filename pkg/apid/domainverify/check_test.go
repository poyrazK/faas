package domainverify_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/domainverify"
)

func TestCheckerBoundsWorkAndCancelledWaiterDoesNotCancelCheck(t *testing.T) {
	var c domainverify.Checker
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- c.Do(ctx, "claim", 1, time.Second, func(ctx context.Context) error {
			calls.Add(1)
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	<-started
	if err := c.Do(t.Context(), "other", 1, time.Second, func(context.Context) error { return nil }); !errors.Is(err, domainverify.ErrBusy) {
		t.Fatalf("unbounded work: %v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	joined := make(chan error, 1)
	joinedCtx := &joiningContext{Context: t.Context(), entered: make(chan struct{})}
	go func() {
		joined <- c.Do(joinedCtx, "claim", 1, time.Second, func(context.Context) error { calls.Add(1); return nil })
	}()
	// The leader is still running; release it only after the cancelled waiter
	// returned. The remaining waiter must be able to finish successfully.
	<-joinedCtx.entered
	close(release)
	if err := <-joined; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate checks: %d", calls.Load())
	}
}

func TestCheckerWakeCoalesces(t *testing.T) {
	var c domainverify.Checker
	for range 100 {
		c.Wake()
	}
	select {
	case <-c.Wakes():
	default:
		t.Fatal("missing wake")
	}
	select {
	case <-c.Wakes():
		t.Fatal("wake queue is unbounded")
	default:
	}
}

type joiningContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *joiningContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

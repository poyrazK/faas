package gateway

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestVMConcurrencyManagerEnforcesPerInstanceLimit(t *testing.T) {
	var delta atomic.Int64
	m := newVMConcurrencyManager(func(_ string, n int64) { delta.Add(n) })

	release, ok := m.tryAcquire("vm-a", "hobby", 2)
	if !ok {
		t.Fatal("first slot was rejected")
	}
	release2, ok := m.tryAcquire("vm-a", "hobby", 2)
	if !ok {
		t.Fatal("second slot was rejected")
	}
	if _, ok := m.tryAcquire("vm-a", "hobby", 2); ok {
		t.Fatal("third slot exceeded the per-instance limit")
	}
	releaseB, ok := m.tryAcquire("vm-b", "hobby", 2)
	if !ok {
		t.Fatal("a second instance must have an independent slot budget")
	}

	release()
	release2()
	if got := delta.Load(); got != 1 {
		// vm-b's slot is intentionally still held; the two vm-a releases
		// must balance their own +1/-1 deltas.
		t.Fatalf("delta after releasing vm-a slots = %d, want 1", got)
	}
	if len(m.gates) != 1 {
		t.Fatalf("idle vm-a gate should be removed while vm-b is active; gates=%d", len(m.gates))
	}
	releaseB()
}

func TestVMConcurrencyManagerWaitsAndHonorsCancellation(t *testing.T) {
	m := newVMConcurrencyManager(nil)
	release, ok := m.tryAcquire("vm-a", "pro", 1)
	if !ok {
		t.Fatal("initial slot was rejected")
	}

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	result := make(chan struct {
		waited bool
		err    error
	}, 1)
	go func() {
		close(started)
		_, waited, err := m.acquire(ctx, "vm-a", "pro", 1)
		result <- struct {
			waited bool
			err    error
		}{waited: waited, err: err}
	}()
	<-started
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case got := <-result:
		if !got.waited {
			t.Error("cancelled request did not observe a saturated gate")
		}
		if !errors.Is(got.err, context.Canceled) {
			t.Errorf("acquire error = %v, want context.Canceled", got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter did not return")
	}
	release()
	if len(m.gates) != 0 {
		t.Fatalf("idle gate was not cleaned up, gates=%d", len(m.gates))
	}
}

func TestVMConcurrencyManagerWakesWaiterOnRelease(t *testing.T) {
	m := newVMConcurrencyManager(nil)
	release, ok := m.tryAcquire("vm-a", "scale", 1)
	if !ok {
		t.Fatal("initial slot was rejected")
	}

	result := make(chan error, 1)
	go func() {
		waitRelease, waited, err := m.acquire(context.Background(), "vm-a", "scale", 1)
		if err == nil && !waited {
			err = context.DeadlineExceeded
		}
		if waitRelease != nil {
			waitRelease()
		}
		result <- err
	}()
	time.Sleep(10 * time.Millisecond)
	release()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("waiter acquire error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("release did not wake the waiting request")
	}
	if len(m.gates) != 0 {
		t.Fatalf("gate remained after all requests drained, gates=%d", len(m.gates))
	}
}

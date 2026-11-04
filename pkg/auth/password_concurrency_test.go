package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Each Argon2id hash allocates 64 MiB; about eight at once OOM-killed apid
// (bug hunt 2026-10-27 H20). No more than api.PasswordHashConcurrency run
// at once.
func TestPasswordHashesAreBounded(t *testing.T) {
	var running, peak atomic.Int32
	var wg sync.WaitGroup
	for range 4 * api.PasswordHashConcurrency {
		wg.Go(func() {
			hashSlots <- struct{}{}
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			running.Add(-1)
			<-hashSlots
		})
	}
	wg.Wait()
	if got := int(peak.Load()); got > api.PasswordHashConcurrency {
		t.Fatalf("concurrent hashes = %d, want at most %d", got, api.PasswordHashConcurrency)
	}
	if cap(hashSlots) != api.PasswordHashConcurrency {
		t.Fatalf("hash slots = %d, want %d", cap(hashSlots), api.PasswordHashConcurrency)
	}
	phc, err := Encode("bounded-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := Verify(phc, "bounded-horse-battery-staple"); !ok || err != nil {
		t.Fatalf("Verify through the bounded path = %v, %v", ok, err)
	}
}

// holdAllHashSlots occupies every slot until the returned func runs.
func holdAllHashSlots(t *testing.T) func() {
	t.Helper()
	for range api.PasswordHashConcurrency {
		hashSlots <- struct{}{}
	}
	return func() {
		for range api.PasswordHashConcurrency {
			<-hashSlots
		}
	}
}

// H96: a waiter whose request ended leaves the queue without hashing, and a
// full queue refuses at once instead of growing without bound.
func TestPasswordHashWaitIsBoundedAndCancellable(t *testing.T) {
	release := holdAllHashSlots(t)
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := VerifyContext(ctx, DummyPHC, "abandoned-request-password")
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("abandoned waiter err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an abandoned waiter kept waiting for a slot")
	}

	waitCtx, stop := context.WithCancel(context.Background())
	defer stop()
	var queued sync.WaitGroup
	for range api.PasswordHashQueueMax {
		queued.Go(func() { _, _ = VerifyContext(waitCtx, DummyPHC, "queued-password") })
	}
	deadline := time.Now().Add(5 * time.Second)
	for hashWaiters.Load() < api.PasswordHashQueueMax {
		if time.Now().After(deadline) {
			t.Fatalf("waiters = %d, want %d", hashWaiters.Load(), api.PasswordHashQueueMax)
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := EncodeContext(context.Background(), "one-more-password-attempt"); !errors.Is(err, ErrPasswordHashBusy) {
		t.Fatalf("attempt past the queue cap err = %v, want ErrPasswordHashBusy", err)
	}
	stop()
	queued.Wait()
	if n := hashWaiters.Load(); n != 0 {
		t.Fatalf("waiters after their requests ended = %d, want 0", n)
	}
}

package main

import (
	"context"
	"testing"
	"time"
)

func TestInvocationCompletionWaiterWaitAndComplete(t *testing.T) {
	w := newInvocationCompletionWaiter(nil, nil)
	result := make(chan error, 1)
	go func() {
		result <- w.Wait(context.Background(), "inv-1", time.Second)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		w.mu.Lock()
		registered := len(w.waiters["inv-1"]) == 1
		w.mu.Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion waiter did not register")
		}
		time.Sleep(time.Millisecond)
	}
	w.completeFromPayload(`{"invocation_id":"inv-1"}`)
	if err := <-result; err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestInvocationCompletionWaiterRecentCompletionClosesRace(t *testing.T) {
	w := newInvocationCompletionWaiter(nil, nil)
	w.completeFromPayload(`{"invocation_id":"inv-2"}`)
	if err := w.Wait(context.Background(), "inv-2", time.Second); err != nil {
		t.Fatalf("Wait after completion: %v", err)
	}
}

func TestInvocationCompletionWaiterTimeoutRemovesWaiter(t *testing.T) {
	w := newInvocationCompletionWaiter(nil, nil)
	if err := w.Wait(context.Background(), "inv-timeout", 5*time.Millisecond); err == nil {
		t.Fatal("Wait returned nil, want timeout")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.waiters) != 0 {
		t.Fatalf("waiters after timeout = %d, want 0", len(w.waiters))
	}
}

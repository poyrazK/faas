package apidserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestInvocationCompletionWaiterWaitAndComplete(t *testing.T) {
	w := apidserver.NewInvocationCompletionWaiter(nil, nil)
	result := make(chan error, 1)
	go func() {
		result <- w.Wait(context.Background(), "inv-1", time.Second)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		(*w.Mu()).Lock()
		registered := len((*w.Waiters())["inv-1"]) == 1
		(*w.Mu()).Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion waiter did not register")
		}
		time.Sleep(time.Millisecond)
	}
	w.CompleteFromPayload(`{"invocation_id":"inv-1"}`)
	if err := <-result; err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestInvocationCompletionWaiterRecentCompletionClosesRace(t *testing.T) {
	w := apidserver.NewInvocationCompletionWaiter(nil, nil)
	w.CompleteFromPayload(`{"invocation_id":"inv-2"}`)
	if err := w.Wait(context.Background(), "inv-2", time.Second); err != nil {
		t.Fatalf("Wait after completion: %v", err)
	}
}

func TestInvocationCompletionWaiterTimeoutRemovesWaiter(t *testing.T) {
	w := apidserver.NewInvocationCompletionWaiter(nil, nil)
	if err := w.Wait(context.Background(), "inv-timeout", 5*time.Millisecond); err == nil {
		t.Fatal("Wait returned nil, want timeout")
	}
	(*w.Mu()).Lock()
	defer (*w.Mu()).Unlock()
	if len((*w.Waiters())) != 0 {
		t.Fatalf("waiters after timeout = %d, want 0", len((*w.Waiters())))
	}
}

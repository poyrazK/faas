package apidserver_test

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestInvocationCompletionWaiterWaitAndComplete(t *testing.T) {
	w := apidserver.NewInvocationCompletionWaiter(nil, nil)
	result := make(chan error, 1)
	go func() {
		result <- w.Wait(context.Background(), "inv-1", time.Second, nil)
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
	if err := w.Wait(context.Background(), "inv-2", time.Second, nil); err != nil {
		t.Fatalf("Wait after completion: %v", err)
	}
}

func TestInvocationCompletionWaiterTimeoutRemovesWaiter(t *testing.T) {
	w := apidserver.NewInvocationCompletionWaiter(nil, nil)
	if err := w.Wait(context.Background(), "inv-timeout", 5*time.Millisecond, nil); err == nil {
		t.Fatal("Wait returned nil, want timeout")
	}
	(*w.Mu()).Lock()
	defer (*w.Mu()).Unlock()
	if len((*w.Waiters())) != 0 {
		t.Fatalf("waiters after timeout = %d, want 0", len((*w.Waiters())))
	}
}

// A completion notification lost while apid's LISTEN connection reconnected
// left the request waiting out its whole timeout. The waiter re-reads the row
// periodically and when the hub reports missed deliveries (bug hunt
// 2026-10-28 B24).
func TestInvocationCompletionWaiterRecoversLostNotification(t *testing.T) {
	w := apidserver.NewInvocationCompletionWaiter(nil, nil)
	start := time.Now()
	if err := w.Wait(context.Background(), "inv-lost", 10*time.Second, func(context.Context) bool { return true }); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("a settled row without a notification took %s, want the recheck interval", elapsed)
	}

	var settled atomic.Bool
	result := make(chan error, 1)
	go func() {
		result <- w.Wait(context.Background(), "inv-resync", 10*time.Second, func(context.Context) bool { return settled.Load() })
	}()
	for {
		(*w.Mu()).Lock()
		registered := len((*w.Waiters())["inv-resync"]) == 1
		(*w.Mu()).Unlock()
		if registered {
			break
		}
		time.Sleep(time.Millisecond)
	}
	settled.Store(true)
	w.WakeAll()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Wait after resync: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("a resync did not make the waiter re-read its row")
	}
}

func TestInvocationCompletionWaiterBoundsRecentCompletions(t *testing.T) {
	w := apidserver.NewInvocationCompletionWaiter(nil, nil)
	for i := range apidserver.InvocationCompletionRecentCap + 10 {
		w.CompleteFromPayload(`{"invocation_id":"inv-` + strconv.Itoa(i) + `"}`)
	}
	(*w.Mu()).Lock()
	defer (*w.Mu()).Unlock()
	if got := w.RecentCount(); got > apidserver.InvocationCompletionRecentCap {
		t.Fatalf("recent completions = %d, want at most %d", got, apidserver.InvocationCompletionRecentCap)
	}
}

// Queue receives LISTENed on a pooled database connection each, so a dozen
// long-polls exhausted apid's pool. They now wait on the shared listener for
// the app's next completion, and their number is capped (bug hunt 2026-10-28
// B21).
func TestInvocationCompletionWaiterDeliversAppCompletionsToReceives(t *testing.T) {
	w := apidserver.NewInvocationCompletionWaiter(nil, nil)
	got := make(chan string, 2)
	for range 2 {
		go func() {
			id, err := w.WaitApp(context.Background(), "app-1", 5*time.Second)
			if err != nil {
				id = "error: " + err.Error()
			}
			got <- id
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for w.AppWaiterCount("app-1") != 2 {
		if time.Now().After(deadline) {
			t.Fatal("receives did not register")
		}
		time.Sleep(time.Millisecond)
	}
	w.CompleteFromPayload(`{"invocation_id":"inv-other","app_id":"app-2"}`)
	w.CompleteFromPayload(`{"invocation_id":"inv-9","app_id":"app-1"}`)
	for range 2 {
		if id := <-got; id != "inv-9" {
			t.Fatalf("receive got %q, want inv-9", id)
		}
	}
	if _, err := w.WaitApp(context.Background(), "app-1", 5*time.Millisecond); err == nil {
		t.Fatal("WaitApp without a completion returned nil, want timeout")
	}
	if n := w.AppWaiterCount("app-1"); n != 0 {
		t.Fatalf("receive waiters left = %d, want 0", n)
	}
}

func TestQueueReceiveSlotsCapPerAccount(t *testing.T) {
	var slots apidserver.QueueReceiveSlots
	var releases []func()
	for range api.QueueReceiveWaitersPerAccount {
		release, _, _, ok := slots.Acquire("acct-1")
		if !ok {
			t.Fatal("receive refused under the cap")
		}
		releases = append(releases, release)
	}
	if _, limit, observed, ok := slots.Acquire("acct-1"); ok || limit != api.QueueReceiveWaitersPerAccount || observed != api.QueueReceiveWaitersPerAccount {
		t.Fatalf("receive past the cap: ok=%v limit=%d observed=%d", ok, limit, observed)
	}
	if _, _, _, ok := slots.Acquire("acct-2"); !ok {
		t.Fatal("another account was refused")
	}
	releases[0]()
	releases[0]() // idempotent
	if _, _, _, ok := slots.Acquire("acct-1"); !ok {
		t.Fatal("a released slot was not reusable")
	}
}

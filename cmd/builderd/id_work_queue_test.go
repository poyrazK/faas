package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestIDWorkQueue_CoalescesAndBoundsNotifications(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan string, 1)
	release := make(chan struct{})
	var mu sync.Mutex
	var handled []string
	q := newIDWorkQueue(ctx, 1, 1, func(_ context.Context, id string) {
		started <- id
		<-release
		mu.Lock()
		handled = append(handled, id)
		mu.Unlock()
	})

	if !q.Enqueue("build-a") {
		t.Fatal("first notification was not accepted")
	}
	select {
	case got := <-started:
		if got != "build-a" {
			t.Fatalf("worker started %q, want build-a", got)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not start build-a")
	}
	if q.Enqueue("build-a") {
		t.Fatal("duplicate notification was accepted while build-a was running")
	}
	if !q.Enqueue("build-b") {
		t.Fatal("build-b should fit in the bounded queue")
	}
	if q.Enqueue("build-c") {
		t.Fatal("build-c should be rejected when the bounded queue is full")
	}

	close(release)
	waitForQueueItem(t, started, "build-b")
	cancel()
	q.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(handled) != 2 {
		t.Fatalf("handled %v, want exactly build-a and build-b", handled)
	}
}

func waitForQueueItem(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("worker started %q, want %s", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("worker did not start %s", want)
	}
}

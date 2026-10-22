package runtime

import (
	"testing"
	"time"
)

// One run's long Seal or Delete must not delay another run's fault restore,
// while a single run's transitions still serialize (ADR-20261019120000).
func TestRunLocksSerializeOnlyTheSameRun(t *testing.T) {
	var l runLocks
	unlockA := l.lock("account", "run-a")

	other := make(chan struct{})
	go func() {
		defer close(other)
		l.lock("account", "run-b")()
	}()
	select {
	case <-other:
	case <-time.After(5 * time.Second):
		t.Fatal("another run waited for run-a's lock")
	}

	same := make(chan struct{})
	go func() {
		defer close(same)
		l.lock("account", "run-a")()
	}()
	select {
	case <-same:
		t.Fatal("the same run acquired its lock concurrently")
	case <-time.After(50 * time.Millisecond):
	}
	unlockA()
	select {
	case <-same:
	case <-time.After(5 * time.Second):
		t.Fatal("the same run never acquired its lock after release")
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.locks) != 0 {
		t.Fatalf("released run locks retained: %d", len(l.locks))
	}
}

package publicrouting

// adr: 20261027085556 — amendment 1: a cold read keeps the routing read bound
// for its first attempt and retries a failure once within the cold bound.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestColdReadRetriesAFailedAttempt(t *testing.T) {
	store := newFakeStore()
	registry, _ := newTestRegistry(store)
	key := newKey()
	store.set(key, "d1")
	store.errOnce = errors.New("acquire connection: pool exhausted")
	view, err := registry.View(t.Context(), key)
	if err != nil || viewDeployment(t, view) != "d1" {
		t.Fatalf("view = %+v err = %v; want the retry's view", view, err)
	}
	if store.readCount() != 2 {
		t.Fatalf("store reads = %d; want the failed attempt and one retry", store.readCount())
	}
}

// The first attempt of a stalled read ends at the routing read bound and the
// retry gets the rest of the cold bound.
func TestColdReadRetriesASlowFirstAttempt(t *testing.T) {
	store := newFakeStore()
	registry, _ := newTestRegistry(store)
	key := newKey()
	store.set(key, "d1")
	block, entered := make(chan struct{}), make(chan struct{}, 2)
	store.block, store.entered = block, entered
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := registry.View(context.Background(), key)
		done <- err
	}()
	<-entered // the first attempt waits until its bound ends it
	<-entered // the retry
	if elapsed := time.Since(started); elapsed < api.TrafficPublicRoutingReadTimeout {
		t.Fatalf("retry began after %v; want the first attempt to run to its bound", elapsed)
	}
	close(block)
	if err := <-done; err != nil {
		t.Fatalf("view after a slow first attempt: %v", err)
	}
}

func TestColdReadGivesUpAtTheColdBound(t *testing.T) {
	store := newFakeStore()
	registry, _ := newTestRegistry(store)
	key := newKey()
	store.set(key, "d1")
	store.block = make(chan struct{})
	started := time.Now()
	_, err := registry.View(context.Background(), key)
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) || elapsed < api.TrafficPublicRoutingColdReadTimeout || elapsed > api.TrafficPublicRoutingColdReadTimeout+time.Second {
		t.Fatalf("stalled store: err = %v after %v; want a deadline at the cold bound", err, elapsed)
	}
	if store.readCount() != 2 {
		t.Fatalf("store reads = %d; want the first attempt and one retry", store.readCount())
	}
}

func TestColdReadDoesNotRetryARefusal(t *testing.T) {
	store := newFakeStore()
	registry, _ := newTestRegistry(store)
	key := newKey()
	bound := errors.New("public routing deployment limit exceeded")
	store.refuse(key, bound)
	if _, err := registry.View(t.Context(), key); !errors.Is(err, bound) {
		t.Fatalf("refused key: %v", err)
	}
	if store.readCount() != 1 {
		t.Fatalf("store reads = %d; a refusal is the store's answer and is not retried", store.readCount())
	}
}

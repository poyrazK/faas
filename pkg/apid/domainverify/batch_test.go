// adr: 520
package domainverify_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/domainverify"
)

func TestRunBatchBoundsConcurrentChecks(t *testing.T) {
	const workers, count = 3, 20
	started := make(chan struct{}, count)
	release := make(chan struct{})
	var completed atomic.Int32
	checks := make([]func(context.Context), count)
	for i := range checks {
		checks[i] = func(ctx context.Context) {
			started <- struct{}{}
			select {
			case <-release:
				completed.Add(1)
			case <-ctx.Done():
			}
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { domainverify.RunBatch(ctx, workers, checks); close(done) }()
	for range workers {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("checks did not run concurrently")
		}
	}
	select {
	case <-started:
		t.Fatal("worker limit exceeded")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("batch did not finish")
	}
	if completed.Load() != count {
		t.Fatalf("completed %d checks, want %d", completed.Load(), count)
	}
}

func TestRunBatchCancelsQueuedChecks(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{}, 20)
	checks := make([]func(context.Context), 20)
	for i := range checks {
		checks[i] = func(ctx context.Context) { started <- struct{}{}; <-ctx.Done() }
	}
	done := make(chan struct{})
	go func() { domainverify.RunBatch(ctx, 2, checks); close(done) }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("checks did not start")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not release the batch")
	}
	if len(started) != 0 {
		t.Fatal("queued checks ran after cancellation")
	}
}

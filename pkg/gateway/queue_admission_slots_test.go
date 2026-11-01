package gateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingFleetQueueAdmission holds every acquire until gate closes, like
// callers queued on one app's advisory lock, and records peak concurrency.
type blockingFleetQueueAdmission struct {
	gate            chan struct{}
	inAcquire, peak atomic.Int32
	releases        atomic.Int32
}

func (b *blockingFleetQueueAdmission) TryAcquireConcurrencyQueueLease(ctx context.Context, _ string, _ int, _ time.Duration) (string, int, bool, error) {
	n := b.inAcquire.Add(1)
	defer b.inAcquire.Add(-1)
	for {
		peak := b.peak.Load()
		if n <= peak || b.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	select {
	case <-b.gate:
		return "lease", 1, true, nil
	case <-ctx.Done():
		return "", 0, false, ctx.Err()
	}
}

func (b *blockingFleetQueueAdmission) ReleaseConcurrencyQueueLease(context.Context, string, string) error {
	b.releases.Add(1)
	return nil
}

func TestBoundQueueAdmissionCapsConcurrentAcquires(t *testing.T) {
	backend := &blockingFleetQueueAdmission{gate: make(chan struct{})}
	admission := boundQueueAdmission(backend)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, ok, err := admission.TryAcquireConcurrencyQueueLease(context.Background(), "app", 10, time.Second); err != nil || !ok {
				t.Errorf("acquire: ok=%v err=%v", ok, err)
			}
		}()
	}
	deadline := time.Now().Add(2 * time.Second)
	for backend.inAcquire.Load() < concurrencyQueueLeaseAcquires && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	// Acquires are saturated; a release must not wait behind them.
	releaseCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := admission.ReleaseConcurrencyQueueLease(releaseCtx, "app", "lease"); err != nil {
		t.Fatalf("release behind saturated acquires: %v", err)
	}

	// A caller whose wait budget ends while every slot is held gets its
	// context error, which acquireVMTarget reports as a queue wait timeout.
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelWait()
	if _, _, _, err := admission.TryAcquireConcurrencyQueueLease(waitCtx, "app", 10, time.Second); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire with no free slot: err=%v, want context.DeadlineExceeded", err)
	}

	close(backend.gate)
	wg.Wait()
	if peak := backend.peak.Load(); peak > concurrencyQueueLeaseAcquires {
		t.Fatalf("peak concurrent acquires = %d, want at most %d", peak, concurrencyQueueLeaseAcquires)
	}
	if got := backend.releases.Load(); got != 1 {
		t.Fatalf("releases = %d, want 1", got)
	}
}

func TestBoundQueueAdmissionKeepsNil(t *testing.T) {
	if boundQueueAdmission(nil) != nil {
		t.Fatal("a nil backend must stay nil so the gateway keeps local-only queueing")
	}
	m := newVMConcurrencyManager(nil)
	m.setQueueAdmission(nil)
	if m.admission != nil {
		t.Fatal("setQueueAdmission(nil) installed a backend")
	}
}

package ci

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type workerHealthLease struct {
	kind  WorkerKind
	slot  int32
	token string
}
type workerHealthStore interface {
	claimWorkerHealth(context.Context, WorkerKind, string, bool, bool, int64) (workerHealthLease, error)
	pulseWorkerHealth(context.Context, workerHealthLease, int64, bool) error
}

type cycleObserver struct{ state atomic.Uint64 }
type cycleObserverKey struct{}

// recordWorkerCycle observes completion, including an empty queue. It cannot
// fail, block, or change the outcome of reconciliation. The low bit records only
// the latest safe failure classification, never an error string.
func recordWorkerCycle(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	if observer, ok := ctx.Value(cycleObserverKey{}).(*cycleObserver); ok {
		for {
			old := observer.state.Load()
			next := ((old >> 1) + 1) << 1
			if err != nil {
				next |= 1
			}
			if observer.state.CompareAndSwap(old, next) {
				return
			}
		}
	}
}

// ObserveWorker runs one configured worker and an independent diagnostic pulse.
// Disabled or missing-dependency workers pulse without running work. Slots are
// bounded and fenced across restarts. Losing a slot never stops actual work.
// Enabled workers which exit stop pulsing, so their evidence eventually stales.
func ObserveWorker(ctx context.Context, store any, kind WorkerKind, enabled, ready bool, run func(context.Context)) {
	if !validWorker(kind) {
		return
	}
	observer := &cycleObserver{}
	workerCtx, cancel := context.WithCancel(context.WithValue(ctx, cycleObserverKey{}, observer))
	defer cancel()
	var pulses sync.WaitGroup
	if writer, ok := store.(workerHealthStore); ok {
		pulses.Go(func() { pulseWorkerLoop(workerCtx, writer, kind, enabled, ready, observer) })
	}
	defer func() { cancel(); pulses.Wait() }()
	if enabled && ready && run != nil {
		run(workerCtx)
	} else {
		<-workerCtx.Done()
	}
}

func pulseWorkerLoop(ctx context.Context, store workerHealthStore, kind WorkerKind, enabled, ready bool, observer *cycleObserver) {
	token := uuid.NewString()
	var lease workerHealthLease
	ticker := time.NewTicker(api.CIWorkerHeartbeatSeconds * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		pulseCtx, cancel := context.WithTimeout(ctx, api.CIHealthTimeoutSeconds*time.Second)
		if lease.token == "" {
			next, err := store.claimWorkerHealth(pulseCtx, kind, token, enabled, ready, int64(observer.state.Load()>>1))
			if err == nil {
				lease = next
			}
		}
		if lease.token != "" {
			current := observer.state.Load()
			err := store.pulseWorkerHealth(pulseCtx, lease, int64(current>>1), current&1 == 1)
			if errors.Is(err, ErrLeaseLost) {
				lease = workerHealthLease{}
				token = uuid.NewString()
			}
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

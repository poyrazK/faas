package sched

import (
	"context"
	"sync"
	"time"
)

// A worker whose every boot fails was re-admitted by worker.singleton the
// moment each instance reached FAILED. On production-us a worker-mode app
// whose image served HTTP failed readiness and cold-booted every ~4 s for ten
// hours, about 20,000 VMs (hunt #10, H10-28). Consecutive failures of one
// deployment now delay the next admission with the service-recovery backoff
// (5 s doubling to 300 s). The first failure still replaces the worker at
// once, and a worker reaching RUNNING resets the count.

// workerBootFailures counts consecutive FAILED worker instances per
// deployment. It lives in the owning schedd's memory, like LivenessWindow; a
// restart forgets it, which costs at most one immediate retry.
type workerBootFailures struct {
	mu    sync.Mutex
	count map[string]int
}

func (w *workerBootFailures) fail(deploymentID string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.count == nil {
		w.count = make(map[string]int)
	}
	w.count[deploymentID]++
	return w.count[deploymentID]
}

func (w *workerBootFailures) reset(deploymentID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.count, deploymentID)
}

// workerRetryDelay is the wait before re-admitting a worker after its
// failures-th consecutive failure.
func workerRetryDelay(failures int) time.Duration {
	if failures <= 1 {
		return 0
	}
	return serviceRecoveryBackoff(failures - 1)
}

func (e *Engine) scheduleWorkerReconcileAfterFailure(ctx context.Context, deploymentID string) {
	if e == nil || deploymentID == "" {
		return
	}
	failures := e.workerBoots.fail(deploymentID)
	delay := workerRetryDelay(failures)
	if delay <= 0 {
		e.scheduleWorkerReconcile(ctx, deploymentID)
		return
	}
	if e.log != nil {
		e.log.Warn("sched: worker failed again; delaying its replacement",
			"deployment", deploymentID, "consecutive_failures", failures, "delay", delay)
	}
	after := e.workerRetryAfter
	if after == nil {
		after = func(d time.Duration, f func()) { time.AfterFunc(d, f) }
	}
	detached := detachedServiceContext(ctx)
	after(delay, func() { e.scheduleWorkerReconcile(detached, deploymentID) })
}

package targets

// adr: 20261018101500 — a queue message wakes an idle worker pool.

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// rememberWorkerPoolApps keeps the queue-driven worker pool apps a tick saw,
// for WakeWorkerPool.
func (t *Trigger) rememberWorkerPoolApps(apps map[string]state.App) {
	t.workerAppsMu.Lock()
	t.workerApps = apps
	t.workerAppsMu.Unlock()
}

// WakeWorkerPool starts a background reconcile of appID's worker pools now
// rather than at the next tick. schedd calls it for each queue message
// (invocation_due): a pool with no serving worker would otherwise wait up to
// a tick to thaw or boot one. It acts only on a queue-driven worker pool app
// the last tick saw, outside admission backoff, with no serving instance in
// the ledger (a frozen worker holds no concurrency); a pool with a serving
// worker leaves the message to that worker's lease and the tick. The run is
// the tick's per-account run. While the account's run is going, which may be
// freezing this pool's worker, the wake is queued for that run, which checks
// it again when it finishes. ctx must outlive the run, as Tick's does. It
// reports whether it started a run.
func (t *Trigger) WakeWorkerPool(ctx context.Context, appID string) bool {
	if t == nil || appID == "" {
		return false
	}
	t.workerAppsMu.Lock()
	app, ok := t.workerApps[appID]
	t.workerAppsMu.Unlock()
	if !ok {
		return false
	}
	pool, ok := t.engine.(ScopedWorkerPoolEngine)
	if !ok || t.deferWorkerPoolWake(app) || !t.wakeNeeded(appID) {
		return false
	}
	var runs workerPoolRuns
	runs.add(pool, app)
	return t.startWorkerPoolRuns(ctx, runs) > 0
}

// wakeNeeded reports whether a woken app is outside admission backoff and has
// no serving instance.
func (t *Trigger) wakeNeeded(appID string) bool {
	if t.admissionBackoffActive(appID, time.Now()) {
		return false
	}
	return t.ledger == nil || t.ledger.Concurrency(appID) == 0
}

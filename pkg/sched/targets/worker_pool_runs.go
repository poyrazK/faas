package targets

// adr: 20261018101500 — worker pools reconcile off the scheduler loop.

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// workerPoolRun is one background reconcile of an account's worker apps.
type workerPoolRun struct {
	pool ScopedWorkerPoolEngine
	apps []state.App
}

// workerPoolRuns collects a tick's worker apps per account, in tick order.
type workerPoolRuns struct {
	order []string
	byKey map[string]*workerPoolRun
}

func (r *workerPoolRuns) add(pool ScopedWorkerPoolEngine, app state.App) {
	key := app.AccountID
	if key == "" {
		key = "app:" + app.ID
	}
	if r.byKey == nil {
		r.byKey = map[string]*workerPoolRun{}
	}
	run := r.byKey[key]
	if run == nil {
		run = &workerPoolRun{pool: pool}
		r.byKey[key] = run
		r.order = append(r.order, key)
	}
	run.apps = append(run.apps, app)
}

// startWorkerPoolRuns reconciles worker pools off the scheduler loop
// (ADR-20261018101500). A reconcile that admits workers returns only when they are
// ready, a whole characterization window for a worker, and the loop also
// runs the reaper, watchdog, cron and job dispatch. Each account's apps run
// in tick order in one background run, so each reconcile computes the
// account's worker cap while no other run changes the account's workers. An
// account whose previous run is still going is skipped and picked up by a
// later tick. Runs use Tick's context, which schedd keeps for the loop's
// lifetime.
func (t *Trigger) startWorkerPoolRuns(ctx context.Context, runs workerPoolRuns) {
	for _, key := range runs.order {
		if !t.claimWorkerPoolRun(key) {
			continue
		}
		run := runs.byKey[key]
		t.workerRuns.Add(1)
		go func() {
			defer t.workerRuns.Done()
			defer t.releaseWorkerPoolRun(key)
			for _, app := range run.apps {
				if ctx.Err() != nil {
					return
				}
				if err := run.pool.ReconcileWorkerPools(ctx, app.ID, workerPoolTriggerTargets); err != nil {
					t.recordAdmissionFailure(app.ID, time.Now())
					t.log.Warn("targets: reconcile worker environments failed", "app_id", app.ID, "err", err)
					continue
				}
				t.clearAdmissionBackoff(app.ID)
			}
		}()
	}
}

func (t *Trigger) claimWorkerPoolRun(key string) bool {
	t.workerRunMu.Lock()
	defer t.workerRunMu.Unlock()
	if t.workerRunning[key] {
		return false
	}
	if t.workerRunning == nil {
		t.workerRunning = map[string]bool{}
	}
	t.workerRunning[key] = true
	return true
}

func (t *Trigger) releaseWorkerPoolRun(key string) {
	t.workerRunMu.Lock()
	delete(t.workerRunning, key)
	t.workerRunMu.Unlock()
}

// waitWorkerPoolRuns blocks until every background worker reconcile has
// returned. Tests call it after Tick.
func (t *Trigger) waitWorkerPoolRuns() {
	t.workerRuns.Wait()
}

package sched

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Worker pools are reconciled on events only: instance transitions, node
// loss, deploy-live and lifecycle changes. ADR-420's continuous recovery
// leaves workers out. A failed worker's delayed replacement
// (worker_boot_backoff.go) is an in-memory timer, so a schedd restart while
// it was pending left the pool empty until another event arrived
// (production-us hunt #10, H10-136). Once per start, schedd reconciles every
// active worker app it owns (ADR docs/adr/1501-worker-startup-reconcile.md).

// workerStartupReconcileDelay lets vmmd and node presence settle after a
// rollout restarts them alongside schedd.
const workerStartupReconcileDelay = 30 * time.Second

func (l *Loop) dispatchWorkerStartupReconcile(ctx context.Context) {
	l.submitWork(workWorkerStartupReconcile, "fleet", func() { l.runWorkerStartupReconcile(ctx) })
}

// runWorkerStartupReconcile walks the owned apps one at a time; each
// ReconcileWorkerApp is a no-op for a pool already at its target.
func (l *Loop) runWorkerStartupReconcile(ctx context.Context) {
	if l == nil || l.engine == nil || l.engine.store == nil {
		return
	}
	store := l.engine.Store()
	var apps []state.App
	var err error
	if owner := l.engine.OwnerNodeID(); owner != "" {
		apps, err = store.ListAppsByNodeID(ctx, owner)
	} else {
		apps, err = store.ListAllApps(ctx)
	}
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			l.log.Warn("worker startup reconcile: list apps", "err", err)
		}
		return
	}
	reconciled := 0
	for _, app := range apps {
		if ctx.Err() != nil {
			return
		}
		if app.Status != state.AppActive || !isWorkerApp(app) || !l.engine.ownsApp(app) {
			continue
		}
		l.engine.ReconcileWorkerApp(ctx, app.ID)
		reconciled++
	}
	l.log.Info("worker startup reconcile: done", "apps", reconciled)
}

// isWorkerApp matches the apps WakeWorkerApp treats as workers.
func isWorkerApp(app state.App) bool {
	return instanceModeForApp(app) == string(state.InstanceModeWorker) || app.WorkloadClass == state.WorkloadClassWorker
}

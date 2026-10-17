package sched

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// workerRecoveryAttempt holds the durable app lease while the existing scoped
// planner applies current intent. Cooldown only suppresses new admissions.
type workerRecoveryAttempt struct {
	lease   state.WorkerRecovery
	status  string
	cooling bool
	scoped  bool
}

func isWorkerApp(app state.App) bool {
	return instanceModeForApp(app) == string(state.InstanceModeWorker) || app.WorkloadClass == state.WorkloadClassWorker
}

func (e *Engine) workerRecoveryOwner() string {
	if owner := e.OwnerNodeID(); owner != "" {
		return owner
	}
	return e.defaultLocalNodeID
}

func workerRecoveryRevision(app state.App, deps []state.Deployment) string {
	// Captured settings are immutable and pinned to deployment IDs. Queue
	// samples and instance IDs must never reset their own failure cooldown.
	data, _ := json.Marshal(struct {
		Revision string
		Policy   *state.ScalingPolicy
		Class    state.WorkloadClass
	}{serviceRecoveryRevision(app, deps), app.ScalingPolicy, app.WorkloadClass})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func workerRecoveryBackoff(failures int) time.Duration {
	delay := time.Duration(api.WorkerRecoveryRetryBaseSeconds) * time.Second
	maximum := time.Duration(api.WorkerRecoveryRetryMaxSeconds) * time.Second
	for n := 1; n < failures && delay < maximum; n++ {
		delay *= 2
	}
	return min(delay, maximum)
}

func (e *Engine) beginWorkerRecovery(ctx context.Context, app state.App, deps []state.Deployment, scoped bool) (*workerRecoveryAttempt, bool, error) {
	if app.Status != state.AppActive {
		return nil, true, nil // Lifecycle cleanup never admits another worker.
	}
	now := e.now().UTC()
	lease, claimed, err := e.store.ClaimWorkerRecovery(ctx, app.ID, e.workerRecoveryOwner(),
		workerRecoveryRevision(app, deps), uuid.NewString(), now,
		now.Add(time.Duration(api.WorkerRecoveryAttemptTimeoutSeconds)*time.Second))
	if err != nil || !claimed {
		return nil, false, err
	}
	return &workerRecoveryAttempt{lease: lease, status: "ready",
		cooling: lease.Failures > 0 && lease.NextAttemptAt.After(now), scoped: scoped}, true, nil
}

func (a *workerRecoveryAttempt) observe(status string) {
	if a == nil {
		return
	}
	// A healthy environment must not erase a neighboring pool's refusal.
	rank := map[string]int{"ready": 0, "starting": 1, "draining": 2,
		"waiting_capacity": 3, "retrying_startup": 4, "waiting_dependency": 5}
	if rank[status] > rank[a.status] {
		a.status = status
	}
}

func (a *workerRecoveryAttempt) allowAdmission(ctx context.Context, now time.Time) bool {
	return a != nil && !a.cooling && ctx.Err() == nil && a.lease.LeaseUntil.After(now)
}

func (e *Engine) completeWorkerRecovery(ctx context.Context, a *workerRecoveryAttempt, attemptErr error) {
	if a == nil {
		return
	}
	now := e.now().UTC()
	row := a.lease
	row.UpdatedAt = now
	row.Status = a.status
	row.NextAttemptAt = now.Add(time.Duration(api.WorkerRecoveryPollIntervalSeconds) * time.Second)
	switch {
	case errors.Is(attemptErr, state.ErrApplicationStandardRuntimeBusy):
		// The outer bounded retry re-reads all scopes after a NOWAIT guard.
		row.Status, row.NextAttemptAt = "starting", now
	case a.cooling && (a.status != "ready" || a.scoped || attemptErr != nil):
		// Repeated events and partial-scope successes neither grow the
		// failure count nor clear or extend a persisted app-wide cooldown.
		row.Status, row.NextAttemptAt = a.lease.Status, a.lease.NextAttemptAt
	case a.scoped && a.lease.Failures > 0 && (a.status == "ready" || a.status == "starting" || a.status == "draining"):
		// A single environment cannot certify recovery of the whole app.
		row.Status, row.NextAttemptAt = a.lease.Status, a.lease.NextAttemptAt
		if row.NextAttemptAt.Before(now) {
			row.NextAttemptAt = now
		}
	case attemptErr != nil || a.status == "waiting_capacity" || a.status == "retrying_startup" || a.status == "waiting_dependency":
		if attemptErr != nil && a.status != "retrying_startup" {
			row.Status = "waiting_dependency"
		}
		row.Failures = min(row.Failures+1, api.WorkerRecoveryFailureCountMax)
		row.NextAttemptAt = now.Add(workerRecoveryBackoff(row.Failures))
	case a.status == "ready":
		row.Failures = 0
		row.NextAttemptAt = now.Add(time.Duration(api.WorkerRecoveryHealthyIntervalSeconds) * time.Second)
	}
	if row.NextAttemptAt.Before(now) {
		row.NextAttemptAt = now // Cooldown may have elapsed during scale-in.
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := e.store.CompleteWorkerRecovery(finishCtx, row.ClaimToken, row); err != nil {
		e.log.Warn("sched: complete worker recovery", "app", row.AppID, "err", err)
	}
}

func (e *Engine) observeWorkerRecoveryFleet(ctx context.Context, appID string, plans map[string]*workerScopePlan, attempt *workerRecoveryAttempt) error {
	if attempt == nil {
		return nil
	}
	rows, err := e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		if plan.target == "" || plan.queueHold {
			continue
		}
		ready, starting, draining := 0, 0, 0
		for _, ins := range rows {
			if ins.DeploymentID != plan.target || ins.Mode != string(state.InstanceModeWorker) {
				continue
			}
			switch state.State(ins.State) {
			case state.StateRunning:
				if !e.workerStopping(ins.ID) {
					ready++
				}
			case state.StateWaking, state.StateColdBooting:
				starting++
			case state.StateDraining:
				draining++
			}
		}
		switch {
		case ready+starting+draining < plan.desired:
			attempt.observe("waiting_capacity")
		case draining > 0:
			attempt.observe("draining")
		case starting > 0:
			attempt.observe("starting")
		}
	}
	return nil
}

func (l *Loop) dispatchWorkerRecovery(ctx context.Context) {
	l.submitWork(workWorkerRecoverySweep, "fleet", func() { l.runWorkerRecovery(ctx) })
}

func (l *Loop) runWorkerRecovery(ctx context.Context) {
	if l == nil || l.engine == nil || l.engine.store == nil {
		return
	}
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	apps, err := l.engine.store.ListWorkerRecoveryApps(listCtx, l.engine.workerRecoveryOwner(),
		l.now().UTC(), api.WorkerRecoveryBatchSize)
	if err != nil {
		l.log.Warn("worker recovery: list due apps", "err", err)
		return
	}
	for _, appID := range apps {
		l.submitWorkerRecovery(ctx, appID)
	}
}

func (l *Loop) submitWorkerRecovery(ctx context.Context, appID string) {
	l.submitWork(workWorkerRecovery, appID, func() { l.engine.ReconcileWorkerApp(ctx, appID) })
}

func (e *Engine) scheduleWorkerAppReconcile(ctx context.Context, appID string) {
	if e == nil || e.store == nil || appID == "" {
		return
	}
	e.mu.Lock()
	submit := e.workerReconcileSubmit
	e.mu.Unlock()
	if submit != nil {
		submit(detachedServiceContext(ctx), appID)
		return
	}
	go e.ReconcileWorkerApp(detachedServiceContext(ctx), appID)
}

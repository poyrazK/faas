package sched

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
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
	scope   string
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

func (e *Engine) beginWorkerRecovery(ctx context.Context, app state.App, deps []state.Deployment, scope string) (*workerRecoveryAttempt, bool, error) {
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
		cooling: lease.Failures > 0 && lease.NextAttemptAt.After(now), scoped: scope != "", scope: scope}, true, nil
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
	// An older writer can change the desired revision without refreshing the
	// diagnostic JSON. These remnants must not block releasing the app lease.
	row.Pools = nil
	for _, pool := range a.lease.Pools {
		if pool.Revision == row.Revision {
			row.Pools = append(row.Pools, pool)
		}
	}
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
	// A busy environment cannot keep moving the full-app sweep into the future.
	// Failed attempts still retain their admission backoff above.
	if a.scoped && attemptErr == nil && (a.status == "ready" || a.status == "starting" || a.status == "draining") &&
		row.NextAttemptAt.After(a.lease.NextAttemptAt) {
		row.NextAttemptAt = a.lease.NextAttemptAt
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
	// Counts live worker rows only (CountsForRAM below).
	rows, err := e.liveInstancesForApp(ctx, appID)
	if err != nil {
		return err
	}
	now := e.now().UTC()
	observations := make([]state.WorkerRecoveryPool, 0, len(plans))
	for _, plan := range plans {
		// A pinned project-release generation shares its scope with the active
		// pool; the recovery summary keeps exactly one pool per scope.
		if plan.pinned {
			continue
		}
		residents := make(map[string]bool, len(plan.workers))
		for id := range plan.residents {
			residents[id] = true
		}
		for _, ins := range plan.workers {
			residents[ins.ID] = true
		}
		ready, starting, draining, targetSlots := 0, 0, 0, 0
		for _, ins := range rows {
			if ins.Mode != string(state.InstanceModeWorker) || !state.State(ins.State).CountsForRAM() ||
				(ins.DeploymentID != plan.target && !residents[ins.ID]) {
				continue
			}
			stopping := e.workerStopping(ins.ID)
			if ins.DeploymentID == plan.target {
				// Retired residents are visible as draining but cannot satisfy
				// the target generation's existing allocation/readiness gate.
				switch state.State(ins.State) {
				case state.StateRunning:
					if !stopping {
						targetSlots++
					}
				case state.StateWaking, state.StateColdBooting, state.StateDraining:
					targetSlots++
				}
			}
			if ins.DeploymentID != plan.target || stopping {
				draining++
				continue
			}
			switch state.State(ins.State) {
			case state.StateRunning:
				ready++
			case state.StateWaking, state.StateColdBooting:
				starting++
			default:
				draining++
			}
		}
		if plan.target != "" || ready+starting+draining > 0 {
			var blockers []api.WorkerAdmissionBlocker
			if targetSlots < plan.desired && !plan.queueHold {
				if !plan.admissionObserved {
					plan.retainAdmissionEvidence(attempt)
				}
				blockers = plan.blockers
			}
			observations = append(observations, state.WorkerRecoveryPool{
				Scope: plan.scope, DeploymentID: plan.target, Revision: attempt.lease.Revision, ObservedAt: now,
				Desired: plan.desired, Running: ready, Starting: starting, Draining: draining, Blockers: blockers})
		}
		if plan.target == "" || plan.queueHold {
			if draining > 0 {
				attempt.observe("draining")
			}
			continue
		}
		switch {
		case targetSlots < plan.desired:
			attempt.observe("waiting_capacity")
		case draining > 0:
			attempt.observe("draining")
		case starting > 0:
			attempt.observe("starting")
		}
	}
	if attempt.scoped {
		for _, old := range attempt.lease.Pools {
			if old.Scope != attempt.scope {
				observations = append(observations, old)
			}
		}
	}
	sort.Slice(observations, func(i, j int) bool { return observations[i].Scope < observations[j].Scope })
	attempt.lease.Pools = observations
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
	if l.engine.ops != nil {
		summary, err := l.engine.store.WorkerRecoverySummary(listCtx, l.engine.workerRecoveryOwner(), l.now().UTC())
		if err != nil {
			l.log.Warn("worker recovery: collect diagnostics", "err", err)
		} else {
			l.engine.ops.SetWorkerRecoverySummary(summary.Apps, summary.Unobserved, summary.Overdue,
				summary.Desired, summary.Running, summary.Starting, summary.Draining, summary.Missing, l.now().UTC())
		}
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

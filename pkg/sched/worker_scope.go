package sched

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ReconcileWorkerPools reads demand separately for each captured environment.
// An app-wide target cannot authorize changing a neighboring worker pool.
func (e *Engine) ReconcileWorkerPools(ctx context.Context, appID, trigger string) error {
	return e.reconcileWorkerScopesWithBusyRetry(ctx, appID, "", nil, trigger)
}

// ReconcileWorkerPoolForScope changes only this environment. Scope is required;
// an empty string is never an instruction to reconcile every environment.
func (e *Engine) ReconcileWorkerPoolForScope(ctx context.Context, appID, scope string, desired int, trigger string) error {
	if scope == "" || api.ValidateScope(scope) != nil || desired < 0 {
		return state.ErrInvalidArgument
	}
	return e.reconcileWorkerScopesWithBusyRetry(ctx, appID, scope, &desired, trigger)
}

// Application-standard guards use NOWAIT row locks so a worker cannot enter
// while its runtime inputs are being changed. A worker-pool pass may already
// have completed earlier scopes or replicas when that guard reports a
// transient busy error. Re-read and reconcile the full plan after a short
// backoff; the failed insert was rolled back, and each pass derives its plan
// from current durable state.
func (e *Engine) reconcileWorkerScopesWithBusyRetry(ctx context.Context, appID, onlyScope string, override *int, trigger string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.WorkerRecoveryAttemptTimeoutSeconds)*time.Second)
	defer cancel()
	const maxAttempts = 4
	for attempt := 0; ; attempt++ {
		err := e.reconcileWorkerScopes(ctx, appID, onlyScope, override, trigger)
		if !errors.Is(err, state.ErrApplicationStandardRuntimeBusy) || attempt+1 == maxAttempts {
			return err
		}
		delay := time.Duration(10<<attempt) * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// WakeWorkerApp handles an explicit wake (`gregale wake`) of a worker app.
// EnsureWake refuses workers as at-capacity because requests never start
// them, so the durable wake request retried forever and a parked worker (a
// manual park, or ADR-079 liveness exhaustion) had no way back: restart needs
// an active app and the singleton reconciler skips parked apps (H8-23). Like
// a wake of a parked request app, it reactivates the app, then lets the
// worker reconciler admit the deployment's worker under the API's wake id.
// It reports handled=false for every non-worker app. A lookup error comes back
// with handled=false: the caller falls through to the regular wake path, which
// reads the app again and reports the failure.
func (e *Engine) WakeWorkerApp(ctx context.Context, appID string) (bool, error) {
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return false, fmt.Errorf("sched: load app for worker wake: %w", err)
	}
	if !e.ownsApp(app) {
		return false, nil
	}
	if instanceModeForApp(app) != string(state.InstanceModeWorker) && app.WorkloadClass != state.WorkloadClassWorker {
		return false, nil
	}
	switch app.Status {
	case state.AppActive:
	case state.AppEvictedCold:
		if _, err := compareAndSetAppStatus(ctx, e.store, appID, state.AppEvictedCold, state.AppActive); err != nil {
			return true, fmt.Errorf("sched: wake worker app %s: reactivate: %w", appID, err)
		}
	default:
		return true, nil
	}
	if err := e.ReconcileWorkerPools(ctx, appID, TriggerAppWake); err != nil {
		return true, fmt.Errorf("sched: wake worker app %s: %w", appID, err)
	}
	return true, nil
}

type workerScopePlan struct {
	scope       string
	app         state.App
	target      string
	desired     int
	signal      string
	cooldown    bool
	queueHold   bool
	pinned      bool
	drainTarget string
	workers     []state.Instance
	// Includes residents already owned by a stop, for diagnostics only.
	residents         map[string]bool
	blockers          []api.WorkerAdmissionBlocker
	admissionObserved bool
}

func (e *Engine) reconcileWorkerScopes(ctx context.Context, appID, onlyScope string, override *int, trigger string) (resultErr error) {
	if trigger == "" {
		trigger = TriggerWorkerPool
	}
	mu := e.serviceAppMutex(appID)
	mu.Lock()
	defer mu.Unlock()
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return err
	}
	if !e.ownsApp(app) {
		return nil
	}
	deployments, err := e.store.LiveDeployments(ctx, appID)
	if err != nil {
		return err
	}
	attempt, proceed, err := e.beginWorkerRecovery(ctx, app, deployments, onlyScope)
	if err != nil || !proceed {
		return err
	}
	plansReady := false
	plans := map[string]*workerScopePlan{}
	defer func() {
		// Capture the resulting fleet even when an admission/startup failed.
		// Never publish a partial plan after a demand/dependency read failure.
		if plansReady {
			if err := e.observeWorkerRecoveryFleet(ctx, appID, plans, attempt); err != nil && resultErr == nil {
				resultErr = err
			}
		}
		e.completeWorkerRecovery(ctx, attempt, resultErr)
	}()
	// The loop below acts only on live worker rows, and the targets trigger
	// reconciles every project worker app each second: read live rows, not
	// the app's whole history (about 8 history reads a second on
	// production-us after rc.285).
	instances, err := e.liveInstancesForApp(ctx, appID)
	if err != nil {
		return err
	}
	byDeployment := make(map[string]state.Deployment, len(deployments))
	for _, dep := range deployments {
		byDeployment[dep.ID] = dep
	}
	planFor := func(scope string) *workerScopePlan {
		if plans[scope] == nil {
			plans[scope] = &workerScopePlan{scope: scope, app: app}
		}
		return plans[scope]
	}
	targets, held, err := e.projectWorkerTargets(ctx, app, deployments)
	if err != nil {
		return err
	}
	if app.Status == state.AppActive {
		for id := range targets {
			dep := byDeployment[id]
			scope := normalizedDeploymentScope(dep.Scope)
			if api.ValidateScope(scope) != nil {
				return state.ErrInvalidArgument
			}
			if onlyScope == "" || onlyScope == scope {
				deployed, err := state.ResolveAppForDeployment(ctx, e.store, app, dep)
				if err != nil {
					return fmt.Errorf("resolve worker policy for %s: %w", scope, err)
				}
				plan := planFor(scope)
				plan.app = deployed
				if instanceModeForApp(deployed) == string(state.InstanceModeWorker) {
					plan.target = id
				}
			}
		}
	}
	pinnedPlans := map[string]*workerScopePlan{}
	if app.Status == state.AppActive && app.ProjectID != "" && app.PreviewOfSlug == "" {
		owner, ok := e.store.(state.ProjectPinnedWorkerStore)
		if !ok {
			return state.ErrConflict
		}
		pinned, err := owner.PinnedProjectReleaseWorkers(ctx, app.ID)
		if err != nil {
			return fmt.Errorf("read pinned worker generations: %w", err)
		}
		for _, id := range pinned {
			if _, active := targets[id]; active {
				continue
			}
			dep, exists := byDeployment[id]
			if !exists || dep.AppID != app.ID || dep.Status != state.DeployLive {
				return state.ErrConflict
			}
			scope := normalizedDeploymentScope(dep.Scope)
			if api.ValidateScope(scope) != nil {
				return state.ErrInvalidArgument
			}
			if onlyScope != "" && onlyScope != scope {
				continue
			}
			deployed, err := state.ResolveAppForDeployment(ctx, e.store, app, dep)
			if err != nil {
				return fmt.Errorf("resolve pinned worker policy: %w", err)
			}
			if instanceModeForApp(deployed) != string(state.InstanceModeWorker) {
				return state.ErrConflict
			}
			plan := &workerScopePlan{scope: scope, app: deployed, target: id, desired: api.ProjectPinnedWorkerReplicas, pinned: true}
			for activeID := range targets {
				if normalizedDeploymentScope(byDeployment[activeID].Scope) == scope {
					plan.drainTarget = activeID
				}
			}
			plans[scope+"\x00"+id] = plan
			pinnedPlans[id] = plan
		}
	}
	if app.Status == state.AppActive {
		for id := range held {
			if _, active := targets[id]; active || pinnedPlans[id] != nil {
				continue
			}
			dep, exists := byDeployment[id]
			if !exists || dep.AppID != app.ID {
				return state.ErrConflict
			}
			if dep.Status != state.DeployLive {
				continue
			}
			scope := normalizedDeploymentScope(dep.Scope)
			if onlyScope != "" && onlyScope != scope {
				continue
			}
			deployed, err := state.ResolveAppForDeployment(ctx, e.store, app, dep)
			if err != nil {
				return err
			}
			if instanceModeForApp(deployed) != string(state.InstanceModeWorker) {
				return state.ErrConflict
			}
			plan := &workerScopePlan{scope: scope, app: deployed, target: id, desired: api.ProjectPinnedWorkerReplicas, pinned: true}
			for activeID := range targets {
				if normalizedDeploymentScope(byDeployment[activeID].Scope) == scope {
					plan.drainTarget = activeID
				}
			}
			plans[scope+"\x00"+id] = plan
			pinnedPlans[id] = plan
		}
	}
	// Resolve retired generations too, before authorizing any stop. Unknown
	// deployment identity is a read failure, not permission to drain the row.
	for _, ins := range instances {
		if _, candidate := held[ins.DeploymentID]; candidate && pinnedPlans[ins.DeploymentID] == nil && app.Status == state.AppActive {
			continue
		}
		if ins.Mode != string(state.InstanceModeWorker) || !state.State(ins.State).CountsForRAM() {
			continue
		}
		dep, exists := byDeployment[ins.DeploymentID]
		if !exists {
			dep, err = e.store.DeploymentByID(ctx, ins.DeploymentID)
			if err != nil {
				return fmt.Errorf("resolve worker environment: %w", err)
			}
			byDeployment[dep.ID] = dep
		}
		scope := normalizedDeploymentScope(dep.Scope)
		if dep.AppID != appID || api.ValidateScope(scope) != nil {
			return state.ErrConflict
		}
		// A deployment still being deployed owns its worker: Prime publishes
		// the worker RUNNING before apid marks the deployment live, and a
		// reconcile in between would stop it as a retired generation. It
		// stays out of the plan, neither kept nor stopped; the account cap
		// still counts it.
		if app.Status == state.AppActive && deploymentInFlight(dep.Status) {
			continue
		}
		if onlyScope == "" || onlyScope == scope {
			if state.State(ins.State) == state.StateMigrating {
				return fmt.Errorf("worker environment %s is migrating: %w", scope, state.ErrConflict)
			}
			plan := pinnedPlans[ins.DeploymentID]
			if plan == nil {
				plan = planFor(scope)
			}
			if plan.residents == nil {
				plan.residents = make(map[string]bool)
			}
			plan.residents[ins.ID] = true
			// A background stop already owns this worker. Leaving it out of
			// the plan means it is neither kept nor stopped twice, while the
			// account cap still counts it until teardown commits.
			if e.workerStopping(ins.ID) {
				continue
			}
			plan.workers = append(plan.workers, ins)
		}
	}
	scopes := make([]string, 0, len(plans))
	for scope, plan := range plans {
		plan.queueHold, err = e.retiredQueueHasInFlightWork(ctx, plan.app, plan.scope)
		if err != nil {
			return fmt.Errorf("retired queue delivery for %s: %w", scope, err)
		}
		if plan.target != "" && !plan.pinned {
			plan.desired, plan.signal, err = e.workerReplicaTargetForScope(ctx, plan.app, plan.scope, len(plan.workers), override)
			if err != nil {
				return fmt.Errorf("worker demand for %s: %w", scope, err)
			}
			if override == nil {
				key := workerPoolKey{appID: app.ID, scope: scope, deploymentID: plan.target}
				plan.desired = e.workerScaleIn.stabilize(key, plan.targetWorkers(), plan.desired, e.now())
			}
			if policy := plan.app.ScalingPolicy; policy != nil && (policy.ScaleOutCooldownS > 0 || policy.ScaleInCooldownS > 0) {
				history, err := e.store.WorkerPoolHistory(ctx, app.ID, plan.target)
				if err != nil {
					return fmt.Errorf("worker cooldown for %s: %w", scope, err)
				}
				plan.applyCooldown(policy, history, e.now())
			}
		}
		scopes = append(scopes, scope)
	}
	// Recover already admitted work before ordinary scale-out consumes the
	// shared account budget. Ordering stays deterministic across restarts.
	sort.Slice(scopes, func(i, j int) bool {
		if plans[scopes[i]].pinned != plans[scopes[j]].pinned {
			return plans[scopes[i]].pinned
		}
		return scopes[i] < scopes[j]
	})
	plansReady = true
	if len(plans) == 0 && !isWorkerApp(app) {
		return nil
	}
	if len(deployments) == 0 && isWorkerApp(app) {
		attempt.observe("waiting_dependency")
	}
	// Every demand read completes before the first mutation. Missing data must
	// not be mistaken for an empty queue and collapse a healthy fleet.
	for _, scope := range scopes {
		plan := plans[scope]
		// Retirement blocks new queue claims. Keep this environment's current
		// residents until previously admitted delivery leases have finished;
		// the hold itself never authorizes another worker or a new generation.
		if plan.queueHold {
			attempt.observe("draining")
			continue
		}
		if err := e.applyWorkerScopePlan(ctx, plan.app, plan, trigger, attempt); err != nil {
			return fmt.Errorf("reconcile worker environment %s: %w", scope, err)
		}
	}
	return nil
}

func (e *Engine) retiredQueueHasInFlightWork(ctx context.Context, app state.App, scope string) (bool, error) {
	bindings, err := e.store.ListQueueBindingHistoryForApp(ctx, app.AccountID, app.ID)
	if err != nil {
		return false, err
	}
	for _, binding := range bindings {
		if binding.RetiredAt == nil {
			continue
		}
		inFlight, err := e.store.QueueBindingHasInFlight(ctx, app.ID, binding.ID, scope)
		if err != nil {
			return false, err
		}
		if inFlight {
			return true, nil
		}
	}
	return false, nil
}

func (e *Engine) applyWorkerScopePlan(ctx context.Context, app state.App, plan *workerScopePlan, trigger string, attempt *workerRecoveryAttempt) (resultErr error) {
	plan.admissionObserved = true
	admitted, atCapacity := false, false
	defer func() {
		if e.ops == nil || trigger != TriggerWorkerPool {
			return
		}
		if admitted {
			e.ops.ObserveScaleUp(app.ID, "admit")
			if plan.signal != "" {
				e.ops.ObserveScaleUpWinningSignal(app.ID, plan.signal)
			}
		} else if plan.cooldown && resultErr == nil {
			e.ops.ObserveScaleUp(app.ID, "cooldown_held")
		} else if atCapacity && resultErr == nil {
			e.ops.ObserveScaleUp(app.ID, "reject_at_cap")
		} else if resultErr == nil {
			e.ops.ObserveScaleUp(app.ID, "no_signal")
		}
	}()
	// Workers are kept in this order, so the tail is stopped first: settled
	// before booting, busy before idle, then oldest first.
	now := e.now()
	leases := e.workerQueueLeases(ctx, app.ID, len(plan.workers) > plan.desired)
	busy := make(map[string]int, len(plan.workers))
	for _, ins := range plan.workers {
		busy[ins.ID] = e.workerBusyRank(ins, now, leases)
	}
	sort.SliceStable(plan.workers, func(i, j int) bool {
		left, right := workerStatePreference(plan.workers[i]), workerStatePreference(plan.workers[j])
		if left != right {
			return left < right
		}
		if left, right := busy[plan.workers[i].ID], busy[plan.workers[j].ID]; left != right {
			return left < right
		}
		if plan.workers[i].StartedAt.Equal(plan.workers[j].StartedAt) {
			return plan.workers[i].ID < plan.workers[j].ID
		}
		return plan.workers[i].StartedAt.Before(plan.workers[j].StartedAt)
	})
	kept := 0
	var stopErr error
	var frozen []state.Instance
	for _, ins := range plan.workers {
		current := state.State(ins.State)
		if ins.DeploymentID == plan.target && kept < plan.desired && current.CountsForConcurrency() {
			kept++
			if current != state.StateRunning {
				attempt.observe("starting")
			}
			continue
		}
		// ADR-20261019103000: the live generation's frozen workers wait to be
		// thawed; FrozenTTL stops them. A surplus running worker freezes when
		// it can and stops otherwise. A project app's surplus workers take the
		// release drain claim and stop instead (ADR-20261019120012): the claim
		// outlives a thaw, so a thawed worker would stay drained.
		if app.ProjectID == "" {
			if current == state.StateFrozen {
				if ins.DeploymentID == plan.target {
					frozen = append(frozen, ins)
					continue
				}
				stopErr = errors.Join(stopErr, e.ParkFrozen(ctx, ins.ID, "superseded"))
				continue
			}
			if current == state.StateRunning && ins.DeploymentID == plan.target && e.freezeSurplusWorker(ctx, app, ins, leases) {
				continue
			}
		}
		if app.ProjectID != "" {
			owner, ok := e.store.(state.ProjectReleaseWorkerStore)
			if !ok {
				return state.ErrConflict
			}
			expectedTarget := plan.target
			if plan.pinned {
				expectedTarget = plan.drainTarget
			}
			claimed, err := owner.ClaimProjectWorkerDrain(ctx, app.ID, ins.ID, expectedTarget)
			if err != nil {
				return err
			}
			if !claimed {
				return state.ErrConflict
			}
			// A worker frozen before its app joined a project.
			if current == state.StateFrozen {
				stopErr = errors.Join(stopErr, e.ParkFrozen(ctx, ins.ID, "superseded"))
				continue
			}
		}
		attempt.observe("draining")
		stopErr = errors.Join(stopErr, e.beginWorkerStop(ctx, ins, e.workerStopOptions(app)))
	}
	if stopErr != nil {
		return stopErr
	}
	// Other environments still count against the account budget. Only this
	// plan's retained workers can be credited back to its available slots.
	desired, blocker, err := e.capWorkerReplicasToAccount(ctx, app, plan.workers, plan.desired)
	if err != nil {
		return err
	}
	if desired < plan.desired && e.stopFrozenWorkersForSlots(ctx, app, plan.workers, plan.desired-desired) {
		if desired, blocker, err = e.capWorkerReplicasToAccount(ctx, app, plan.workers, plan.desired); err != nil {
			return err
		}
	}
	if desired < plan.desired {
		plan.observeAdmission(e.now(), *blocker)
		attempt.observe("waiting_capacity")
		atCapacity = true
	}
	// A frozen worker resumes in milliseconds; thaw the most recently frozen
	// before booting new ones (ADR-20261019103000).
	sort.SliceStable(frozen, func(i, j int) bool { return frozen[i].LastRequestAt.After(frozen[j].LastRequestAt) })
	for _, ins := range frozen {
		if kept >= desired {
			break
		}
		if e.thawFrozenWorker(ctx, app, plan.target, ins) {
			admitted = true
			kept++
		}
	}
	for kept < desired {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !attempt.allowAdmission(ctx, e.now()) {
			plan.observeAdmissionHold(attempt)
			attempt.observe("waiting_capacity")
			atCapacity = true
			break
		}
		dep, err := e.store.DeploymentByID(ctx, plan.target)
		if err != nil {
			return err
		}
		if dep.AppID != app.ID || dep.Status != state.DeployLive || normalizedDeploymentScope(dep.Scope) != plan.scope {
			return state.ErrConflict
		}
		result, err := e.AdmitInstanceForDeployment(ctx, app.ID, dep.ID, plan.scope, trigger)
		if err != nil {
			var problem *api.Problem
			if errors.Is(err, state.ErrNodeCapacity) || (errors.As(err, &problem) && problem.Code == api.CodeCapacity) {
				plan.observeAdmission(e.now(), admissionBlockers(err)...)
				attempt.observe("waiting_capacity")
				atCapacity = true
				break
			}
			attempt.observe("retrying_startup")
			return err
		}
		if result.AtCapacity {
			blockers := result.admissionBlockers
			if len(blockers) == 0 {
				blockers = []api.WorkerAdmissionBlocker{{Code: "admission_refused"}}
			}
			plan.observeAdmission(e.now(), blockers...)
			attempt.observe("waiting_capacity")
			atCapacity = true
			break
		}
		admitted = true
		kept++
	}
	return nil
}

// targetWorkers counts the live generation's workers that hold a slot.
func (p *workerScopePlan) targetWorkers() int {
	current := 0
	for _, ins := range p.workers {
		if ins.DeploymentID == p.target && state.State(ins.State).CountsForConcurrency() {
			current++
		}
	}
	return current
}

func (p *workerScopePlan) applyCooldown(policy *state.ScalingPolicy, history state.WorkerPoolHistory, now time.Time) {
	if policy == nil {
		return
	}
	current := p.targetWorkers()
	// A cold environment or a new generation must not inherit a neighbor's
	// cooldown. Retained rows preserve admission/termination history even
	// after a recent replica has gone away.
	if current == 0 {
		return
	}
	seconds := 0
	var lastEvent time.Time
	if p.desired > current {
		seconds = policy.ScaleOutCooldownS
		lastEvent = history.LastAdmissionAt
	} else if p.desired < current {
		seconds = policy.ScaleInCooldownS
		lastEvent = history.LastTerminationAt
	}
	if seconds > 0 && !lastEvent.IsZero() && now.Before(lastEvent.Add(time.Duration(seconds)*time.Second)) {
		p.cooldown = p.desired > current
		p.desired = current
	}
}

func (e *Engine) workerReplicaTargetForScope(ctx context.Context, app state.App, scope string, currentReplicas int, override *int) (int, string, error) {
	account, err := state.AppResourceAccount(ctx, e.store, app)
	if err != nil {
		return 0, "", err
	}
	limits, ok := api.LimitsFor(account.Plan)
	if !ok || limits.WorkerReplicasMax <= 0 {
		return 0, "", nil
	}
	max := app.MaxConcurrency
	if max <= 0 || max > limits.MaxConcurrency {
		max = limits.MaxConcurrency
	}
	if app.ScalingPolicy != nil && app.ScalingPolicy.MaxInstances > 0 && app.ScalingPolicy.MaxInstances < max {
		max = app.ScalingPolicy.MaxInstances
	}
	if limits.WorkerReplicasMax < max {
		max = limits.WorkerReplicasMax
	}
	minAllowed := 1
	if replicas := app.Manifest.WorkerReplicas; replicas != nil {
		minAllowed = replicas.Min
		if minAllowed < 0 {
			minAllowed = 0
		}
		if replicas.Max > 0 && replicas.Max < max {
			max = replicas.Max
		}
	}
	desired := minAllowed
	signal := ""
	if override != nil {
		desired = *override
	} else if policy := app.ScalingPolicy; policy != nil {
		queueTarget, haveQueue := policy.TargetFor(api.ScalingMetricQueueDepth)
		lagTarget, haveLag := policy.TargetFor(api.ScalingMetricQueueLag)
		customTargets := make([]state.ScalingTarget, 0, len(policy.EffectiveTargets()))
		for _, target := range policy.EffectiveTargets() {
			if target.Metric == api.ScalingMetricCustom {
				customTargets = append(customTargets, target)
			}
		}
		if haveQueue && queueTarget > 0 {
			desired, err = e.workerQueueDemandForScope(ctx, app, scope, queueTarget, max)
			if err != nil {
				return 0, "", err
			}
			if desired > 0 {
				signal = api.ScalingMetricQueueDepth
			}
		}
		// The existing broker surface has no environment identity. Its lag
		// can authorize demand only in the producer's default environment.
		haveLagSignal := false
		if haveLag && lagTarget > 0 {
			haveSignal := false
			if e.brokerLag != nil && scope == state.DefaultInvocationDeploymentScope(app) {
				lag, available, err := e.brokerLag.BrokerLag(ctx, app.ID)
				if err != nil {
					return 0, "", err
				}
				haveSignal = available
				haveLagSignal = available
				if available {
					if lagDesired := int(math.Ceil(float64(lag) / lagTarget)); lagDesired > desired {
						desired = lagDesired
						signal = api.ScalingMetricQueueLag
					}
				}
			}
			if !haveQueue && !haveSignal {
				if len(customTargets) == 0 {
					return 0, "", fmt.Errorf("queue_lag has no environment-scoped signal: %w", state.ErrConflict)
				}
			}
		}
		if len(customTargets) > 0 {
			// Custom metrics are app-wide and do not include a deployment
			// environment dimension. They may only authorize the default
			// invocation pool; applying them to every project environment
			// would multiply the same backlog across neighboring pools.
			if scope != state.DefaultInvocationDeploymentScope(app) {
				return 0, "", fmt.Errorf("custom worker scaling metrics have no environment scope: %w", state.ErrConflict)
			}
			rows, err := e.store.ListCustomMetrics(ctx, app.ID)
			if err != nil {
				return 0, "", fmt.Errorf("read worker custom metrics: %w", err)
			}
			stored := make(map[string]state.CustomMetric, len(rows))
			for _, row := range rows {
				if row.Kind == state.CustomMetricKindCounter {
					continue // ADR-745: counters are never a backlog signal
				}
				stored[row.Name] = row
			}
			now := e.now()
			freshness := time.Duration(api.CustomMetricFreshnessSeconds) * time.Second
			haveCustomSignal := false
			for _, target := range customTargets {
				row, ok := stored[target.Name]
				if !ok || now.Sub(row.ObservedAt) > freshness {
					continue
				}
				haveCustomSignal = true
				demand := max
				if ratio := math.Ceil(row.Value / target.Value); ratio < float64(max) {
					demand = int(ratio)
				}
				if demand > desired {
					desired = demand
					signal = api.ScalingMetricCustom
				}
			}
			if currentReplicas > desired && mcpTasksScaleToZeroConfigured(app) {
				heartbeat, hasHeartbeat := stored[mcpTasksObserverHeartbeatMetric]
				heartbeatFresh := hasHeartbeat && now.Sub(heartbeat.ObservedAt) <= freshness && heartbeat.Value == 1
				observerReady, readinessErr := e.mcpTasksObserverReady(ctx, app)
				if readinessErr != nil {
					return 0, "", fmt.Errorf("verify MCP task observer readiness: %w", readinessErr)
				}
				if !observerReady || !heartbeatFresh {
					// A recent worker-side backlog gauge cannot authorize scale-in
					// when the dedicated observer is unavailable. Keep this pool at
					// its current size until both the linked app and heartbeat recover.
					desired = currentReplicas
					signal = ""
				}
			}
			if !haveCustomSignal && !haveQueue && !haveLagSignal {
				if currentReplicas > 0 {
					return 0, "", fmt.Errorf("custom worker scaling metric is missing or stale: %w", state.ErrConflict)
				}
				// Start the configured minimum so an in-process publisher can
				// emit its first reading. Once a fleet exists, missing data is
				// not evidence that the queue is empty and must not scale it in.
			}
		}
		if policy.MinInstances > desired {
			desired = policy.MinInstances
		}
	}
	if desired < minAllowed {
		desired = minAllowed
	}
	if desired > max {
		desired = max
	}
	return desired, signal, nil
}

const mcpTasksObserverHeartbeatMetric = "mcp_tasks_observer_heartbeat"

func mcpTasksScaleToZeroConfigured(app state.App) bool {
	scaling := app.Manifest.WorkerReplicas
	if scaling == nil || scaling.Min != 0 || scaling.Metric != api.ScalingMetricCustom || scaling.Name != "mcp_tasks_outstanding" {
		return false
	}
	if app.ScalingPolicy == nil {
		return false
	}
	for _, target := range app.ScalingPolicy.EffectiveTargets() {
		if target.Metric == api.ScalingMetricCustom && target.Name == "mcp_tasks_outstanding" {
			return true
		}
	}
	return false
}

func (e *Engine) mcpTasksObserverReady(ctx context.Context, worker state.App) (bool, error) {
	scaling := worker.Manifest.WorkerReplicas
	if scaling == nil || scaling.ObserverAppSlug == "" {
		return false, nil
	}
	observer, err := e.store.AppBySlug(ctx, scaling.ObserverAppSlug)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if observer.ID == worker.ID || observer.AccountID != worker.AccountID || observer.Status != state.AppActive ||
		(observer.Manifest.ExecutionMode != api.ExecutionModeWorker && observer.WorkloadClass != state.WorkloadClassWorker) ||
		observer.Manifest.WorkerReplicas == nil || observer.Manifest.WorkerReplicas.Min < 1 {
		return false, nil
	}
	liveDeployments, err := e.store.LiveDeployments(ctx, observer.ID)
	if err != nil {
		return false, err
	}
	if len(liveDeployments) == 0 {
		return false, nil
	}
	live := make(map[string]struct{}, len(liveDeployments))
	for _, deployment := range liveDeployments {
		live[deployment.ID] = struct{}{}
	}
	instances, err := e.liveInstancesForApp(ctx, observer.ID)
	if err != nil {
		return false, err
	}
	for _, instance := range instances {
		if instance.Mode == string(state.InstanceModeWorker) && instance.State == string(state.StateRunning) {
			if _, ok := live[instance.DeploymentID]; ok {
				return true, nil
			}
		}
	}
	return false, nil
}

// workerQueueDemandForScope reads each queue's depth only up to the depth that
// saturates maxReplicas workers: the caller caps desired at maxReplicas, and
// a full count would read the whole backlog on every reconcile.
func (e *Engine) workerQueueDemandForScope(ctx context.Context, app state.App, scope string, target float64, maxReplicas int) (int, error) {
	bindings, err := e.store.ListQueueBindingHistoryForApp(ctx, app.AccountID, app.ID)
	if err != nil {
		return 0, err
	}
	limit := workerQueueDepthLimit(target, maxReplicas)
	if len(bindings) == 0 {
		depth, err := e.store.QueueDepthInScope(ctx, app.ID, scope, limit)
		return int(math.Ceil(float64(depth) / target)), err
	}
	desired := 0
	var environment *state.ProjectEnvironment
	for _, binding := range bindings {
		if !binding.Enabled || binding.RetiredAt != nil || binding.DeploymentScope != "" && binding.DeploymentScope != scope {
			continue
		}
		if binding.DeploymentScope != "" {
			if app.ProjectID == "" {
				continue
			}
			if environment == nil {
				found, err := e.store.ProjectEnvironmentBySlug(ctx, app.AccountID, app.ProjectID, scope)
				if errors.Is(err, state.ErrNotFound) {
					continue
				}
				if err != nil {
					return 0, err
				}
				environment = &found
			}
			if environment.ID != binding.EnvironmentID {
				continue
			}
		}
		depth, err := e.store.QueueDepthForBindingInScope(ctx, app.ID, binding.ID, scope, limit)
		if err != nil {
			return 0, err
		}
		demand := int(math.Ceil(float64(depth) / target))
		if binding.MaxConcurrency > 0 && demand > binding.MaxConcurrency {
			demand = binding.MaxConcurrency
		}
		desired += demand
	}
	return desired, nil
}

// workerQueueDepthLimit is the depth at which ceil(depth/target) reaches
// maxReplicas; no deeper read can change the pool's size. A target stored
// before api.ScalingQueueTargetMax existed reads as that maximum, so it
// cannot make each reconcile count a whole queue.
func workerQueueDepthLimit(target float64, maxReplicas int) int {
	target = min(target, api.ScalingQueueTargetMax)
	limit := math.Ceil(target * float64(max(maxReplicas, 1)))
	if limit < 1 || math.IsNaN(limit) {
		return 1
	}
	return int(min(limit, float64(1<<20)))
}

func (e *Engine) capWorkerReplicasToAccount(ctx context.Context, app state.App, current []state.Instance, desired int) (int, *api.WorkerAdmissionBlocker, error) {
	if desired <= 0 {
		return desired, nil, nil
	}
	account, err := state.AppResourceAccount(ctx, e.store, app)
	if err != nil {
		return 0, nil, err
	}
	limits, ok := api.LimitsFor(account.Plan)
	if !ok || limits.WorkerReplicasMax <= 0 {
		// A plan change can arrive after demand planning. Usage has not
		// been read here, so omit numeric evidence rather than assume zero.
		b := api.WorkerAdmissionBlocker{Code: "account_worker_limit"}
		return 0, &b, nil
	}
	all, err := e.store.ListInstancesForAccount(ctx, account.ResourceOwnerID())
	if err != nil {
		return 0, nil, err
	}
	currentIDs := make(map[string]bool, len(current))
	for _, ins := range current {
		currentIDs[ins.ID] = true
	}
	otherWorkers, used := 0, 0
	for _, ins := range all {
		if ins.Mode == string(state.InstanceModeWorker) && state.State(ins.State).CountsForRAM() {
			used++
			if !currentIDs[ins.ID] {
				otherWorkers++
			}
		}
	}
	available := limits.WorkerReplicasMax - otherWorkers
	if available < 0 {
		available = 0
	}
	if desired > available {
		b := limitBlocker("account_worker_limit", "replicas", int64(used), int64(limits.WorkerReplicasMax))
		return available, &b, nil
	}
	return desired, nil, nil
}

// deploymentInFlight reports whether a deployment is still being deployed:
// not yet live, and neither failed, superseded nor cancelled.
func deploymentInFlight(status state.DeploymentStatus) bool {
	switch status {
	case state.DeployPending, state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting:
		return true
	}
	return false
}

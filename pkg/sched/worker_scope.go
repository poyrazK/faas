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
	return e.reconcileWorkerScopes(ctx, appID, "", nil, trigger)
}

// ReconcileWorkerPoolForScope changes only this environment. Scope is required;
// an empty string is never an instruction to reconcile every environment.
func (e *Engine) ReconcileWorkerPoolForScope(ctx context.Context, appID, scope string, desired int, trigger string) error {
	if scope == "" || api.ValidateScope(scope) != nil || desired < 0 {
		return state.ErrInvalidArgument
	}
	return e.reconcileWorkerScopes(ctx, appID, scope, &desired, trigger)
}

type workerScopePlan struct {
	scope    string
	target   string
	desired  int
	signal   string
	cooldown bool
	workers  []state.Instance
}

func (e *Engine) reconcileWorkerScopes(ctx context.Context, appID, onlyScope string, override *int, trigger string) error {
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
	instances, err := e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return err
	}
	byDeployment := make(map[string]state.Deployment, len(deployments))
	for _, dep := range deployments {
		byDeployment[dep.ID] = dep
	}
	plans := map[string]*workerScopePlan{}
	planFor := func(scope string) *workerScopePlan {
		if plans[scope] == nil {
			plans[scope] = &workerScopePlan{scope: scope}
		}
		return plans[scope]
	}
	if app.Status == state.AppActive && instanceModeForApp(app) == string(state.InstanceModeWorker) {
		for id := range workerDeploymentTargets(deployments) {
			scope := normalizedDeploymentScope(byDeployment[id].Scope)
			if api.ValidateScope(scope) != nil {
				return state.ErrInvalidArgument
			}
			if onlyScope == "" || onlyScope == scope {
				planFor(scope).target = id
			}
		}
	}
	// Resolve retired generations too, before authorizing any stop. Unknown
	// deployment identity is a read failure, not permission to drain the row.
	for _, ins := range instances {
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
		if onlyScope == "" || onlyScope == scope {
			if state.State(ins.State) == state.StateMigrating {
				return fmt.Errorf("worker environment %s is migrating: %w", scope, state.ErrConflict)
			}
			planFor(scope).workers = append(planFor(scope).workers, ins)
		}
	}
	scopes := make([]string, 0, len(plans))
	for scope, plan := range plans {
		if plan.target != "" {
			plan.desired, plan.signal, err = e.workerReplicaTargetForScope(ctx, app, scope, override)
			if err != nil {
				return fmt.Errorf("worker demand for %s: %w", scope, err)
			}
			if policy := app.ScalingPolicy; policy != nil && (policy.ScaleOutCooldownS > 0 || policy.ScaleInCooldownS > 0) {
				history, err := e.store.WorkerPoolHistory(ctx, app.ID, plan.target)
				if err != nil {
					return fmt.Errorf("worker cooldown for %s: %w", scope, err)
				}
				plan.applyCooldown(policy, history, e.now())
			}
		}
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	// Every demand read completes before the first mutation. Missing data must
	// not be mistaken for an empty queue and collapse a healthy fleet.
	for _, scope := range scopes {
		plan := plans[scope]
		if err := e.applyWorkerScopePlan(ctx, app, plan, trigger); err != nil {
			return fmt.Errorf("reconcile worker environment %s: %w", scope, err)
		}
	}
	return nil
}

func (e *Engine) applyWorkerScopePlan(ctx context.Context, app state.App, plan *workerScopePlan, trigger string) (resultErr error) {
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
	sort.SliceStable(plan.workers, func(i, j int) bool {
		left, right := workerStatePreference(plan.workers[i]), workerStatePreference(plan.workers[j])
		if left != right {
			return left < right
		}
		if plan.workers[i].StartedAt.Equal(plan.workers[j].StartedAt) {
			return plan.workers[i].ID < plan.workers[j].ID
		}
		return plan.workers[i].StartedAt.Before(plan.workers[j].StartedAt)
	})
	kept := 0
	var stopErr error
	for _, ins := range plan.workers {
		if ins.DeploymentID == plan.target && kept < plan.desired && state.State(ins.State).CountsForConcurrency() {
			kept++
			continue
		}
		stopErr = errors.Join(stopErr, e.stopManagedWorker(ctx, ins.ID, e.workerStopOptions(app)))
	}
	if stopErr != nil {
		return stopErr
	}
	// Other environments still count against the account budget. Only this
	// plan's retained workers can be credited back to its available slots.
	desired, err := e.capWorkerReplicasToAccount(ctx, app, plan.workers, plan.desired)
	if err != nil {
		return err
	}
	if desired < plan.desired {
		atCapacity = true
	}
	for kept < desired {
		dep, err := e.store.DeploymentByID(ctx, plan.target)
		if err != nil {
			return err
		}
		if dep.AppID != app.ID || dep.Status != state.DeployLive || normalizedDeploymentScope(dep.Scope) != plan.scope {
			return state.ErrConflict
		}
		result, err := e.AdmitInstanceForDeployment(ctx, app.ID, dep.ID, plan.scope, trigger)
		if err != nil {
			return err
		}
		if result.AtCapacity {
			atCapacity = true
			break
		}
		admitted = true
		kept++
	}
	return nil
}

func (p *workerScopePlan) applyCooldown(policy *state.ScalingPolicy, history state.WorkerPoolHistory, now time.Time) {
	if policy == nil {
		return
	}
	current := 0
	for _, ins := range p.workers {
		if ins.DeploymentID == p.target && state.State(ins.State).CountsForConcurrency() {
			current++
		}
	}
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

func (e *Engine) workerReplicaTargetForScope(ctx context.Context, app state.App, scope string, override *int) (int, string, error) {
	account, err := e.store.AccountByID(ctx, app.AccountID)
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
		if haveQueue && queueTarget > 0 {
			desired, err = e.workerQueueDemandForScope(ctx, app, scope, queueTarget)
			if err != nil {
				return 0, "", err
			}
			if desired > 0 {
				signal = api.ScalingMetricQueueDepth
			}
		}
		// The existing broker surface has no environment identity. Its lag
		// can authorize demand only in the producer's default environment.
		if haveLag && lagTarget > 0 {
			haveSignal := false
			if e.brokerLag != nil && scope == state.DefaultInvocationDeploymentScope(app) {
				lag, available, err := e.brokerLag.BrokerLag(ctx, app.ID)
				if err != nil {
					return 0, "", err
				}
				haveSignal = available
				if available {
					if lagDesired := int(math.Ceil(float64(lag) / lagTarget)); lagDesired > desired {
						desired = lagDesired
						signal = api.ScalingMetricQueueLag
					}
				}
			}
			if !haveQueue && !haveSignal {
				return 0, "", fmt.Errorf("queue_lag has no environment-scoped signal: %w", state.ErrConflict)
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

func (e *Engine) workerQueueDemandForScope(ctx context.Context, app state.App, scope string, target float64) (int, error) {
	bindings, err := e.store.ListQueueBindingsForApp(ctx, app.AccountID, app.ID)
	if err != nil {
		return 0, err
	}
	if len(bindings) == 0 {
		stats, err := e.store.QueueStateInScope(ctx, app.ID, scope)
		return int(math.Ceil(float64(stats.Depth) / target)), err
	}
	desired := 0
	for _, binding := range bindings {
		if !binding.Enabled {
			continue
		}
		stats, err := e.store.QueueStateForQueueInScope(ctx, app.ID, binding.QueueName, scope)
		if err != nil {
			return 0, err
		}
		demand := int(math.Ceil(float64(stats.Depth) / target))
		if binding.MaxConcurrency > 0 && demand > binding.MaxConcurrency {
			demand = binding.MaxConcurrency
		}
		desired += demand
	}
	return desired, nil
}

func (e *Engine) capWorkerReplicasToAccount(ctx context.Context, app state.App, current []state.Instance, desired int) (int, error) {
	if desired <= 0 {
		return desired, nil
	}
	account, err := e.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return 0, err
	}
	limits, ok := api.LimitsFor(account.Plan)
	if !ok || limits.WorkerReplicasMax <= 0 {
		return 0, nil
	}
	all, err := e.store.ListInstancesForAccount(ctx, app.AccountID)
	if err != nil {
		return 0, err
	}
	currentIDs := make(map[string]bool, len(current))
	for _, ins := range current {
		currentIDs[ins.ID] = true
	}
	otherWorkers := 0
	for _, ins := range all {
		if ins.Mode == string(state.InstanceModeWorker) && state.State(ins.State).CountsForRAM() && !currentIDs[ins.ID] {
			otherWorkers++
		}
	}
	available := limits.WorkerReplicasMax - otherWorkers
	if available < 0 {
		available = 0
	}
	if desired > available {
		desired = available
	}
	return desired, nil
}

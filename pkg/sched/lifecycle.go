package sched

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

const serviceRolloutTimeout = 10 * time.Minute

func instanceModeForApp(app state.App) string {
	switch app.Manifest.ExecutionMode {
	case api.ExecutionModeService:
		return string(state.InstanceModeService)
	case api.ExecutionModeWorker:
		return string(state.InstanceModeWorker)
	case api.ExecutionModeJob:
		return string(state.InstanceModeJob)
	default:
		return string(state.InstanceModeNormal)
	}
}

func desiredServiceReplicas(manifest state.AppManifest) int {
	if manifest.ServiceReplicas == nil {
		return 1
	}
	return manifest.ServiceReplicas.Desired
}

// serviceReplicaStatus is the scheduler's readiness projection for one
// service deployment. A service target is about healthy serving capacity, not
// merely rows that still hold a RAM reservation:
//
//   - ready replicas are RUNNING after the boot readiness gate;
//   - starting replicas are still in WAKING/COLD_BOOTING;
//   - draining replicas are leaving service (SNAPSHOTTING/MIGRATING); and
//   - unavailable replicas are terminal or parked.
//
// The in-flight buckets are deliberately separate from ready. This prevents a
// scale-down from parking the last healthy replica just because a replacement
// is still booting, while still preventing the reconciler from admitting
// duplicate replacements for work already in flight.
type serviceReplicaStatus struct {
	ready       int
	starting    int
	draining    int
	unavailable int
}

func (s serviceReplicaStatus) inFlight() int {
	return s.starting + s.draining
}

func (s serviceReplicaStatus) managed() int {
	return s.ready + s.inFlight()
}

func normalizedInstanceMode(mode string) string {
	if mode == "" {
		return string(state.InstanceModeNormal)
	}
	return mode
}

func instanceModeMatchesApp(app state.App, ins state.Instance) bool {
	return normalizedInstanceMode(ins.Mode) == instanceModeForApp(app)
}

// listServiceReplicas returns every service-mode row for a deployment,
// including terminal and parked history. The reconciler needs those states to
// distinguish an unavailable replica from a boot already in flight; filtering
// to CountsForConcurrency would collapse that distinction.
func listServiceReplicas(ctx context.Context, store state.Store, appID, deploymentID string) ([]state.Instance, error) {
	instances, err := store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	replicas := make([]state.Instance, 0, len(instances))
	for _, ins := range instances {
		if ins.DeploymentID == deploymentID && ins.Mode == string(state.InstanceModeService) {
			replicas = append(replicas, ins)
		}
	}
	return replicas, nil
}

func classifyServiceReplicas(replicas []state.Instance) serviceReplicaStatus {
	var status serviceReplicaStatus
	for _, replica := range replicas {
		switch state.State(replica.State) {
		case state.StateRunning:
			status.ready++
		case state.StateWaking, state.StateColdBooting:
			status.starting++
		case state.StateSnapshotting, state.StateMigrating:
			status.draining++
		default:
			status.unavailable++
		}
	}
	return status
}

func listLiveDeploymentInstances(ctx context.Context, store state.Store, appID, deploymentID string) ([]state.Instance, error) {
	instances, err := store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	live := make([]state.Instance, 0, len(instances))
	for _, ins := range instances {
		if ins.DeploymentID != deploymentID || !state.State(ins.State).CountsForConcurrency() {
			continue
		}
		live = append(live, ins)
	}
	return live, nil
}

func detachedServiceContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func (e *Engine) scheduleServiceReconcile(ctx context.Context, deploymentID string) {
	if e == nil || e.store == nil || deploymentID == "" {
		return
	}
	go e.ReconcileServiceDeployment(detachedServiceContext(ctx), deploymentID)
}

func (e *Engine) serviceMutex(deploymentID string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.serviceMu == nil {
		e.serviceMu = make(map[string]*sync.Mutex)
	}
	mu, ok := e.serviceMu[deploymentID]
	if !ok {
		mu = &sync.Mutex{}
		e.serviceMu[deploymentID] = mu
	}
	return mu
}

func (e *Engine) serviceAppMutex(appID string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.serviceAppMu == nil {
		e.serviceAppMu = make(map[string]*sync.Mutex)
	}
	mu, ok := e.serviceAppMu[appID]
	if !ok {
		mu = &sync.Mutex{}
		e.serviceAppMu[appID] = mu
	}
	return mu
}

// allocateServiceReplicaTargets distributes one scope's app-level service
// target across its live deployment generations. Traffic weights are used as
// the allocation signal, but every positive-weight generation gets one
// replica when the target is large enough to support it. This keeps a small
// canary warm without allowing a second live generation to duplicate the
// full target.
//
// The caller supplies deployments in deterministic order (LiveDeployments is
// newest first). That order is the final tie-breaker for equal weights and
// equal remainders.
func allocateServiceReplicaTargets(deployments []state.Deployment, desired int) map[string]int {
	targets := make(map[string]int, len(deployments))
	for _, dep := range deployments {
		targets[dep.ID] = 0
	}
	if len(deployments) == 0 || desired <= 0 {
		return targets
	}
	if len(deployments) == 1 {
		targets[deployments[0].ID] = desired
		return targets
	}

	weights := make([]int64, len(deployments))
	positive := make([]int, 0, len(deployments))
	var totalWeight int64
	for i, dep := range deployments {
		if dep.TrafficPercent <= 0 {
			continue
		}
		weights[i] = int64(dep.TrafficPercent)
		totalWeight += weights[i]
		positive = append(positive, i)
	}
	if len(positive) == 0 || totalWeight <= 0 {
		// Corrupt or legacy traffic metadata must not make every generation
		// unavailable. LiveDeployments is newest first, so prefer the newest
		// generation while the traffic split is repaired.
		targets[deployments[0].ID] = desired
		return targets
	}

	if desired < len(positive) {
		// The target cannot give every generation a floor. Prefer the highest
		// traffic weights, with the stable input order as the tie-breaker.
		sort.SliceStable(positive, func(i, j int) bool {
			return weights[positive[i]] > weights[positive[j]]
		})
		for _, index := range positive[:desired] {
			targets[deployments[index].ID]++
		}
		return targets
	}

	for _, index := range positive {
		targets[deployments[index].ID] = 1
	}
	remaining := desired - len(positive)
	if remaining == 0 {
		return targets
	}

	type remainder struct {
		index  int
		value  int64
		weight int64
	}
	remainders := make([]remainder, 0, len(positive))
	assigned := 0
	for _, index := range positive {
		numerator := int64(remaining) * weights[index]
		whole := int(numerator / totalWeight)
		targets[deployments[index].ID] += whole
		assigned += whole
		remainders = append(remainders, remainder{
			index:  index,
			value:  numerator % totalWeight,
			weight: weights[index],
		})
	}

	// Largest remainder makes the integer allocation sum exactly to desired.
	// Higher traffic wins exact ties so a 25/75 split with four replicas is
	// allocated 1/3 rather than depending on map or database iteration order.
	sort.SliceStable(remainders, func(i, j int) bool {
		if remainders[i].value != remainders[j].value {
			return remainders[i].value > remainders[j].value
		}
		return remainders[i].weight > remainders[j].weight
	})
	for i := assigned; i < remaining; i++ {
		index := remainders[i-assigned].index
		targets[deployments[index].ID]++
	}
	return targets
}

func normalizedDeploymentScope(scope string) string {
	if scope == "" {
		return "default"
	}
	return scope
}

// serviceReplicaTargets computes allocations independently per deployment
// scope. Canary generations in one scope share that scope's target; unrelated
// staging/prod scopes retain their own service capacity.
func (e *Engine) serviceReplicaTargets(ctx context.Context, app state.App, deployments []state.Deployment) (map[string]int, error) {
	rules, err := e.store.ListMirrorRules(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	mirrorDeployments := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		mirrorDeployments[rule.MirrorDeploymentID] = struct{}{}
	}

	byScope := make(map[string][]state.Deployment)
	for _, dep := range deployments {
		if _, mirror := mirrorDeployments[dep.ID]; mirror {
			continue
		}
		scope := normalizedDeploymentScope(dep.Scope)
		byScope[scope] = append(byScope[scope], dep)
	}

	targets := make(map[string]int, len(deployments))
	desired := desiredServiceReplicas(app.Manifest)
	for _, scoped := range byScope {
		for deploymentID, target := range allocateServiceReplicaTargets(scoped, desired) {
			targets[deploymentID] = target
		}
	}
	return targets, nil
}

func serviceRolloutScope(dep state.Deployment) string {
	return normalizedDeploymentScope(dep.Scope)
}

func activeServiceRollouts(deployments []state.Deployment) map[string]state.Deployment {
	rollouts := make(map[string]state.Deployment)
	for _, dep := range deployments {
		if dep.Status != state.DeployLive || !state.IsServiceRollout(dep) {
			continue
		}
		scope := serviceRolloutScope(dep)
		current, ok := rollouts[scope]
		if !ok || dep.CreatedAt.After(current.CreatedAt) ||
			(dep.CreatedAt.Equal(current.CreatedAt) && dep.ID > current.ID) {
			rollouts[scope] = dep
		}
	}
	return rollouts
}

func previousServiceDeployment(rollout state.Deployment, deployments []state.Deployment) state.Deployment {
	var previous state.Deployment
	for _, dep := range deployments {
		if dep.ID == rollout.ID || dep.Status != state.DeployLive ||
			serviceRolloutScope(dep) != serviceRolloutScope(rollout) ||
			state.IsServiceRollout(dep) {
			continue
		}
		if !rollout.CreatedAt.IsZero() && dep.CreatedAt.After(rollout.CreatedAt) {
			continue
		}
		if previous.ID == "" || dep.CreatedAt.After(previous.CreatedAt) ||
			(dep.CreatedAt.Equal(previous.CreatedAt) && dep.ID > previous.ID) {
			previous = dep
		}
	}
	return previous
}

func serviceRolloutStartedAt(dep state.Deployment) time.Time {
	if dep.RolloutStartedAt != nil {
		return *dep.RolloutStartedAt
	}
	return dep.CreatedAt
}

func serviceRolloutTimedOut(dep state.Deployment, now time.Time) bool {
	started := serviceRolloutStartedAt(dep)
	return !started.IsZero() && now.Sub(started) >= serviceRolloutTimeout
}

func (e *Engine) emitServiceRolloutChange(ctx context.Context, appID, deploymentID string, status state.DeploymentStatus) {
	payload, _ := json.Marshal(map[string]any{
		"kind":          "service_rollout",
		"status":        string(status),
		"app_id":        appID,
		"deployment_id": deploymentID,
	})
	if err := e.Notifier().Notify(ctx, db.NotifyDeploymentChanged, string(payload)); err != nil {
		e.log.Warn("sched: notify service rollout change", "app", appID, "deployment", deploymentID, "status", status, "err", err)
	}
}

// drainServiceDeploymentInstances releases a generation after the traffic
// handoff. A successful generation is parked so its snapshot remains a useful
// rollback cache; an aborted, never-serving generation is hard-stopped.
func (e *Engine) drainServiceDeploymentInstances(ctx context.Context, deploymentID string, preserveSnapshot bool) {
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service rollout drain", "deployment", deploymentID, "err", err)
		}
		return
	}
	replicas, err := listServiceReplicas(ctx, e.store, dep.AppID, deploymentID)
	if err != nil {
		e.log.Warn("sched: list service rollout drain", "deployment", deploymentID, "err", err)
		return
	}
	for _, replica := range replicas {
		fresh, err := e.store.InstanceByID(ctx, replica.ID)
		if err != nil {
			continue
		}
		switch state.State(fresh.State) {
		case state.StateRunning:
			if preserveSnapshot {
				if err := e.Park(ctx, fresh.ID); err != nil {
					e.log.Warn("sched: park old service rollout replica", "instance", fresh.ID, "deployment", deploymentID, "err", err)
				}
				continue
			}
			if err := e.Evict(ctx, fresh.ID); err != nil {
				e.log.Warn("sched: stop aborted service rollout replica", "instance", fresh.ID, "deployment", deploymentID, "err", err)
			}
		case state.StateWaking, state.StateColdBooting:
			destroyCtx := context.WithoutCancel(ctx)
			if err := e.timedDestroy(destroyCtx, fresh.NodeID, fresh.ID, DestroyTimeout); err != nil {
				e.log.Warn("sched: destroy service rollout wake", "instance", fresh.ID, "deployment", deploymentID, "err", err)
				continue
			}
			e.ledger.Release(fresh.ID)
			e.transition(ctx, fresh.ID, fresh.AppID, state.StateStopped)
		}
	}
}

func (e *Engine) finishServiceRollout(ctx context.Context, app state.App, rollout, previous state.Deployment) bool {
	updated, err := e.store.FinalizeServiceRollout(ctx, rollout.ID)
	if err != nil {
		if !errors.Is(err, state.ErrServiceRolloutInvalid) && !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: finalize service rollout", "app", app.ID, "deployment", rollout.ID, "err", err)
		}
		return false
	}
	if previous.ID != "" {
		e.drainServiceDeploymentInstances(ctx, previous.ID, true)
	}
	e.emitServiceRolloutChange(ctx, app.ID, updated.ID, updated.Status)
	return true
}

func (e *Engine) abortServiceRollout(ctx context.Context, app state.App, rollout state.Deployment, reason string) bool {
	updated, err := e.store.AbortServiceRollout(ctx, rollout.ID, reason)
	if err != nil {
		if !errors.Is(err, state.ErrServiceRolloutInvalid) && !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: abort service rollout", "app", app.ID, "deployment", rollout.ID, "err", err)
		}
		return false
	}
	e.drainServiceDeploymentInstances(ctx, rollout.ID, false)
	live, listErr := e.store.LiveDeployments(ctx, app.ID)
	if listErr == nil && len(live) > 0 {
		e.emitServiceRolloutChange(ctx, app.ID, live[0].ID, live[0].Status)
	} else {
		e.emitServiceRolloutChange(ctx, app.ID, updated.ID, updated.Status)
	}
	return true
}

// reconcileServiceRollout advances one scope by at most one ready replica per
// pass. The old generation keeps the remainder of the desired capacity until
// the new generation proves readiness; the total temporary surge is one.
func (e *Engine) reconcileServiceRollout(ctx context.Context, app state.App, rollout state.Deployment, deployments []state.Deployment) {
	desired := desiredServiceReplicas(app.Manifest)
	previous := previousServiceDeployment(rollout, deployments)
	replicas, err := listServiceReplicas(ctx, e.store, app.ID, rollout.ID)
	if err != nil {
		e.log.Warn("sched: list new service rollout replicas", "app", app.ID, "deployment", rollout.ID, "err", err)
		return
	}
	status := classifyServiceReplicas(replicas)
	if status.ready >= desired {
		e.finishServiceRollout(ctx, app, rollout, previous)
		return
	}
	if serviceRolloutTimedOut(rollout, time.Now().UTC()) {
		e.abortServiceRollout(ctx, app, rollout, "readiness timeout")
		return
	}
	if previous.ID == "" {
		e.convergeServiceReplicasToTarget(ctx, rollout.ID, desired, true)
	} else {
		newTarget := status.ready + 1
		if newTarget > desired {
			newTarget = desired
		}
		oldTarget := desired - status.ready
		if oldTarget < 0 {
			oldTarget = 0
		}
		e.convergeServiceReplicasToTarget(ctx, previous.ID, oldTarget, false)
		e.convergeServiceReplicasToTarget(ctx, rollout.ID, newTarget, true)
		// max_concurrency is also the service app's replica ceiling. A
		// rollout normally uses one bounded surge slot, but the API permits
		// the common exact-fit shape (max_concurrency == desired). If the
		// first replacement admission hit that ceiling, release one ready
		// predecessor replica and retry. This keeps exact-fit services from
		// hanging forever while retaining the predecessor's snapshot for a
		// rollback if the replacement later fails.
		if app.MaxConcurrency > 0 && e.ledger.Concurrency(app.ID) >= app.MaxConcurrency {
			ready, readErr := listServiceReplicas(ctx, e.store, app.ID, rollout.ID)
			if readErr == nil && classifyServiceReplicas(ready).managed() < newTarget {
				if e.parkOneServiceReplica(ctx, app.ID, previous.ID) {
					e.convergeServiceReplicasToTarget(ctx, rollout.ID, newTarget, true)
				}
			}
		}
	}
	// Admission may synchronously reach RUNNING. Re-read so a fast boot can
	// complete the rollout without waiting for a second notification.
	ready, readErr := listServiceReplicas(ctx, e.store, app.ID, rollout.ID)
	if readErr == nil && classifyServiceReplicas(ready).ready >= desired {
		e.finishServiceRollout(ctx, app, rollout, previous)
	}
}

// ReconcileServiceDeployment restores the app's service allocation after a
// deployment or instance notification. The allocation is app-scoped because
// a canary and its predecessor are both live during a rollout.
func (e *Engine) ReconcileServiceDeployment(ctx context.Context, deploymentID string) {
	ctx = detachedServiceContext(ctx)
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service deployment for app reconcile", "deployment", deploymentID, "err", err)
		}
		return
	}
	e.ReconcileServiceApp(ctx, dep.AppID)
}

// ReconcileServiceApp applies one globally consistent service allocation to
// every live deployment of an app. Surplus is parked for all generations
// before any deficit is admitted, which makes rollout capacity available even
// when the predecessor currently occupies the entire app quota.
func (e *Engine) ReconcileServiceApp(ctx context.Context, appID string) {
	ctx = detachedServiceContext(ctx)
	reconcileMu := e.serviceAppMutex(appID)
	reconcileMu.Lock()
	defer reconcileMu.Unlock()

	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service app", "app", appID, "err", err)
		}
		return
	}
	if app.Status != state.AppActive {
		return
	}
	deployments, err := e.store.LiveDeployments(ctx, appID)
	if err != nil {
		e.log.Warn("sched: list live service deployments", "app", appID, "err", err)
		return
	}
	handledScopes := make(map[string]struct{})
	if instanceModeForApp(app) == string(state.InstanceModeService) {
		rollouts := activeServiceRollouts(deployments)
		scopes := make([]string, 0, len(rollouts))
		for scope := range rollouts {
			scopes = append(scopes, scope)
		}
		sort.Strings(scopes)
		for _, scope := range scopes {
			handledScopes[scope] = struct{}{}
			e.reconcileServiceRollout(ctx, app, rollouts[scope], deployments)
		}
	}
	targets := make(map[string]int, len(deployments))
	if instanceModeForApp(app) == string(state.InstanceModeService) {
		var targetErr error
		targets, targetErr = e.serviceReplicaTargets(ctx, app, deployments)
		if targetErr != nil {
			e.log.Warn("sched: allocate service replicas", "app", appID, "err", targetErr)
			return
		}
		for _, dep := range deployments {
			if _, handled := handledScopes[serviceRolloutScope(dep)]; handled {
				delete(targets, dep.ID)
			}
		}
	} else {
		// A mode switch away from service still needs to drain the old
		// service rows. Non-service instances are intentionally ignored by
		// convergeServiceReplicasToTarget's service-row filter.
		for _, dep := range deployments {
			targets[dep.ID] = 0
		}
	}
	// First release capacity from generations above their allocation.
	for _, dep := range deployments {
		target, ok := targets[dep.ID]
		if !ok {
			continue
		}
		e.convergeServiceReplicasToTarget(ctx, dep.ID, target, false)
	}
	// Then fill deficits with the capacity made available above.
	for _, dep := range deployments {
		target, ok := targets[dep.ID]
		if !ok {
			continue
		}
		e.convergeServiceReplicasToTarget(ctx, dep.ID, target, true)
	}
}

func (e *Engine) convergeServiceReplicas(ctx context.Context, deploymentID string) {
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service deployment", "deployment", deploymentID, "err", err)
		}
		return
	}
	app, err := e.store.AppByID(ctx, dep.AppID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service app", "app", dep.AppID, "deployment", deploymentID, "err", err)
		}
		return
	}
	if app.Status != state.AppActive {
		return
	}
	deployments, err := e.store.LiveDeployments(ctx, dep.AppID)
	if err != nil {
		e.log.Warn("sched: list live service deployments", "app", dep.AppID, "err", err)
		return
	}
	targets := make(map[string]int, len(deployments))
	if instanceModeForApp(app) == string(state.InstanceModeService) {
		var targetErr error
		targets, targetErr = e.serviceReplicaTargets(ctx, app, deployments)
		if targetErr != nil {
			e.log.Warn("sched: allocate service replicas", "app", dep.AppID, "err", targetErr)
			return
		}
	} else {
		for _, liveDep := range deployments {
			targets[liveDep.ID] = 0
		}
	}
	desired, ok := targets[deploymentID]
	if !ok {
		return
	}
	e.convergeServiceReplicasToTarget(ctx, deploymentID, desired, true)
}

func (e *Engine) convergeServiceReplicasToTarget(ctx context.Context, deploymentID string, desired int, admit bool) {
	if desired < 0 {
		desired = 0
	}
	reconcileMu := e.serviceMutex(deploymentID)
	reconcileMu.Lock()
	defer reconcileMu.Unlock()

	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service deployment", "deployment", deploymentID, "err", err)
		}
		return
	}
	if dep.Status != state.DeployLive {
		return
	}
	app, err := e.store.AppByID(ctx, dep.AppID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Warn("sched: load service app", "app", dep.AppID, "deployment", deploymentID, "err", err)
		}
		return
	}
	if app.Status != state.AppActive {
		return
	}
	serviceMode := instanceModeForApp(app) == string(state.InstanceModeService)
	if serviceMode {
		mirror, mirrorErr := e.isMirrorDeployment(ctx, dep.AppID, dep.ID)
		if mirrorErr != nil {
			e.log.Warn("sched: check service mirror deployment", "app", dep.AppID, "deployment", dep.ID, "err", mirrorErr)
			return
		}
		if mirror {
			return
		}
	}
	if serviceMode {
		instances, listErr := listLiveDeploymentInstances(ctx, e.store, dep.AppID, dep.ID)
		if listErr != nil {
			e.log.Warn("sched: list incompatible service instances", "deployment", dep.ID, "err", listErr)
			return
		}
		e.drainIncompatibleServiceReplicas(ctx, instances)
	}
	serviceReplicas, err := listServiceReplicas(ctx, e.store, dep.AppID, deploymentID)
	if err != nil {
		e.log.Warn("sched: list service replicas", "deployment", deploymentID, "err", err)
		return
	}
	status := classifyServiceReplicas(serviceReplicas)
	// Never trade away healthy capacity while a replacement is still
	// booting. A deployment can temporarily exceed desired during a
	// scale-down, but it must not temporarily fall below desired because a
	// not-yet-ready replica was counted as equivalent to RUNNING.
	if excess := status.ready - desired; excess > 0 {
		parked := e.parkSurplusServiceReplicas(ctx, serviceReplicas, excess)
		status.ready -= parked
	}
	if !admit || desired <= 0 {
		return
	}
	for status.managed() < desired {
		result, admitErr := e.AdmitInstanceForDeployment(
			ctx, dep.AppID, dep.ID, dep.Scope, TriggerServiceReplica,
		)
		if admitErr != nil {
			e.log.Warn("sched: admit service replica", "app", dep.AppID, "deployment", dep.ID, "err", admitErr)
			return
		}
		if result.AtCapacity {
			e.log.Debug("sched: service replica admission at capacity", "app", dep.AppID, "deployment", dep.ID)
			return
		}
		// AdmitInstanceForDeployment returns only after the new replica has
		// reached RUNNING (or has failed), so count the successful result as
		// ready for this pass. A later asynchronous transition will trigger
		// another reconciliation for failures and replacements.
		status.ready++
	}
}

func (e *Engine) isMirrorDeployment(ctx context.Context, appID, deploymentID string) (bool, error) {
	rules, err := e.store.ListMirrorRules(ctx, appID)
	if err != nil {
		return false, err
	}
	for _, rule := range rules {
		if rule.MirrorDeploymentID == deploymentID {
			return true, nil
		}
	}
	return false, nil
}

// drainIncompatibleServiceReplicas removes request/worker/job instances that
// were already live when the app switched into service mode. They cannot count
// toward the service target and must release their resident admission before a
// service replica can be admitted. In-flight rows are destroyed as well: if a
// request-mode wake were allowed to finish after the mode switch, it could
// consume capacity as an extra RUNNING VM and never trigger a service
// reconciliation because its instance mode is not service.
func (e *Engine) drainIncompatibleServiceReplicas(ctx context.Context, instances []state.Instance) int {
	removed := 0
	for _, ins := range instances {
		if normalizedInstanceMode(ins.Mode) == string(state.InstanceModeService) ||
			ins.Mode == string(state.InstanceModeMirror) {
			continue
		}
		fresh, err := e.store.InstanceByID(ctx, ins.ID)
		if err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				e.log.Warn("sched: reload incompatible service instance", "instance", ins.ID, "deployment", ins.DeploymentID, "err", err)
			}
			continue
		}
		switch state.State(fresh.State) {
		case state.StateRunning:
			if err := e.Park(ctx, fresh.ID); err != nil {
				e.log.Warn("sched: park incompatible service instance", "instance", fresh.ID, "deployment", fresh.DeploymentID, "err", err)
				continue
			}
			removed++
		case state.StateWaking, state.StateColdBooting:
			// A mode switch is an explicit lifecycle change. Destroy the
			// in-flight VM and close its reservation so the subsequent
			// service admit cannot be rejected by a stale request wake.
			destroyCtx := context.WithoutCancel(ctx)
			if err := e.timedDestroy(destroyCtx, fresh.NodeID, fresh.ID, DestroyTimeout); err != nil {
				e.log.Warn("sched: destroy incompatible service wake", "instance", fresh.ID, "deployment", fresh.DeploymentID, "err", err)
				continue
			}
			e.ledger.Release(fresh.ID)
			e.transition(ctx, fresh.ID, fresh.AppID, state.StateStopped)
			removed++
		}
	}
	return removed
}

// parkSurplusServiceReplicas drains the oldest RUNNING service instances first.
// ListInstancesForApp returns newest rows first, so walking backwards preserves
// the freshest replicas and avoids taking every replacement from the same boot
// generation during a scale-down.
func (e *Engine) parkSurplusServiceReplicas(ctx context.Context, replicas []state.Instance, excess int) int {
	parked := 0
	for i := len(replicas) - 1; i >= 0 && parked < excess; i-- {
		ins := replicas[i]
		if state.State(ins.State) != state.StateRunning {
			continue
		}
		if err := e.Park(ctx, ins.ID); err != nil {
			e.log.Warn("sched: park surplus service replica", "instance", ins.ID, "deployment", ins.DeploymentID, "err", err)
			continue
		}
		parked++
	}
	return parked
}

// parkOneServiceReplica releases one per-app concurrency slot for an
// exact-fit rolling replacement. The caller has already confirmed that a
// replacement admission could not fit under max_concurrency; only a RUNNING
// predecessor is eligible because parking an in-flight wake would require a
// different destroy path and would make the rollout's capacity accounting
// ambiguous.
func (e *Engine) parkOneServiceReplica(ctx context.Context, appID, deploymentID string) bool {
	replicas, err := listServiceReplicas(ctx, e.store, appID, deploymentID)
	if err != nil {
		return false
	}
	for _, replica := range replicas {
		if state.State(replica.State) != state.StateRunning {
			continue
		}
		if err := e.Park(ctx, replica.ID); err != nil {
			e.log.Warn("sched: park predecessor for exact-fit service rollout", "instance", replica.ID, "deployment", deploymentID, "err", err)
			continue
		}
		return true
	}
	return false
}

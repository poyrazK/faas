package sched

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

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
	targets := make(map[string]int, len(deployments))
	if instanceModeForApp(app) == string(state.InstanceModeService) {
		var targetErr error
		targets, targetErr = e.serviceReplicaTargets(ctx, app, deployments)
		if targetErr != nil {
			e.log.Warn("sched: allocate service replicas", "app", appID, "err", targetErr)
			return
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

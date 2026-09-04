package sched

import (
	"context"
	"errors"
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

// ReconcileServiceDeployment restores the desired count for one live
// deployment. Callers may use it from notification handlers; the work is
// detached from the notification context so a reconnect or shutdown does not
// strand a replacement that has already been requested.
func (e *Engine) ReconcileServiceDeployment(ctx context.Context, deploymentID string) {
	e.convergeServiceReplicas(detachedServiceContext(ctx), deploymentID)
}

// ReconcileServiceApp applies lifecycle changes to every live scope of an app.
func (e *Engine) ReconcileServiceApp(ctx context.Context, appID string) {
	ctx = detachedServiceContext(ctx)
	deployments, err := e.store.LiveDeployments(ctx, appID)
	if err != nil {
		e.log.Warn("sched: list live service deployments", "app", appID, "err", err)
		return
	}
	for _, dep := range deployments {
		e.ReconcileServiceDeployment(ctx, dep.ID)
	}
}

func (e *Engine) convergeServiceReplicas(ctx context.Context, deploymentID string) {
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
	desired := 0
	if serviceMode {
		desired = desiredServiceReplicas(app.Manifest)
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
	if desired <= 0 {
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

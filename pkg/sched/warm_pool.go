package sched

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// warmPoolRestoreMaxPerTick bounds the amount of resident capacity one
// reaper pass can create for a single app. A later pass continues the
// convergence, keeping a large PATCH from monopolising the scheduler loop.
const warmPoolRestoreMaxPerTick = 4

// ReconcileWarmPool converges the selected environment's paused resident VM
// pool. An unscoped caller selects production. Warm rows are separate from serving
// replicas: they reserve RAM/vCPU through KindWarmPool, but do not consume the
// app's serving concurrency until the resume/promotion path lands.
//
// The reconciler restores only an already-published snapshot (warm tier when
// the plan permits it, otherwise init tier). A deployment that has never
// produced a usable snapshot remains at zero and is retried on the next tick;
// cold boot is intentionally not introduced into the warm-pool path.
func (e *Engine) ReconcileWarmPool(ctx context.Context, appID string) error {
	if e == nil || e.store == nil || appID == "" {
		return nil
	}
	ctx = detachedServiceContext(ctx)
	mu := e.serviceAppMutex(appID)
	mu.Lock()
	defer mu.Unlock()
	budget := warmPoolRestoreMaxPerTick
	return e.reconcileWarmPoolLocked(ctx, appID, &budget)
}

// ReconcileEnvironmentWarmPools recovers every deployed environment after a
// missed notification. All pools share the existing per-app restore budget and
// physical node ledger. A failed environment does not prevent sibling recovery.
func (e *Engine) ReconcileEnvironmentWarmPools(ctx context.Context, appID string) error {
	if e == nil || e.store == nil || appID == "" {
		return nil
	}
	ctx = detachedServiceContext(ctx)
	mu := e.serviceAppMutex(appID)
	mu.Lock()
	defer mu.Unlock()
	deployments, err := e.store.LiveDeployments(ctx, appID)
	if err != nil {
		return fmt.Errorf("sched: warm pool: environment inventory: %w", err)
	}
	instances, err := e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return fmt.Errorf("sched: warm pool: paused inventory: %w", err)
	}
	scopes := map[string]bool{"production": true}
	addScope := func(dep state.Deployment) error {
		if dep.AppID != appID || api.ValidateScope(normalizedDeploymentScope(dep.Scope)) != nil {
			return state.ErrConflict
		}
		if !reaperProductionScope(dep.Scope) {
			scopes[dep.Scope] = true
		}
		return nil
	}
	for _, dep := range deployments {
		if err := addScope(dep); err != nil {
			return err
		}
	}
	// Retained paused rows keep a disabled or superseded pool discoverable.
	for _, ins := range instances {
		if ins.State != string(state.StateWarm) {
			continue
		}
		dep, err := e.store.DeploymentByID(ctx, ins.DeploymentID)
		if err != nil {
			return fmt.Errorf("sched: warm pool: retained owner: %w", err)
		}
		if ins.AppID != appID {
			return state.ErrConflict
		}
		if err := addScope(dep); err != nil {
			return err
		}
	}
	ordered := make([]string, 0, len(scopes))
	for scope := range scopes {
		if scope != "production" {
			ordered = append(ordered, scope)
		}
	}
	sort.Strings(ordered)
	ordered = append([]string{"production"}, ordered...)
	budget := warmPoolRestoreMaxPerTick
	var errs []error
	for _, scope := range ordered {
		if err := e.reconcileWarmPoolLocked(WithScope(ctx, scope), appID, &budget); err != nil {
			errs = append(errs, fmt.Errorf("environment %s: %w", scope, err))
		}
	}
	return errors.Join(errs...)
}

// The caller holds the app's reconciliation mutex and owns the fill budget.
func (e *Engine) reconcileWarmPoolLocked(ctx context.Context, appID string, budget *int) error {
	scope := normalizedDeploymentScope(ScopeFrom(ctx))
	if api.ValidateScope(scope) != nil {
		return state.ErrInvalidArgument
	}
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("sched: warm pool: load app: %w", err)
	}
	if !e.ownsApp(app) {
		return nil
	}
	acct, err := e.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return fmt.Errorf("sched: warm pool: load account: %w", err)
	}
	// Keep the plan-labelled gauge aligned with durable WARM rows even when
	// reconciliation exits early (disabled pool, plan gate, or no snapshot).
	defer e.refreshWarmPoolSizeGauge(ctx, acct.Plan, appID)
	instances, err := e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return fmt.Errorf("sched: warm pool: list instances: %w", err)
	}
	// Resolve this environment's release and tested settings before computing
	// its desired pool. Paused instances from a sibling environment do not count
	// toward this target and must never enter this reconciler's cleanup set.
	dep, depErr := state.ResolveEnvironmentDeployment(ctx, e.store, appID, scope)
	if depErr != nil && !errors.Is(depErr, state.ErrNotFound) {
		return fmt.Errorf("sched: warm pool: live deployment: %w", depErr)
	}
	if depErr == nil {
		app, err = state.ResolveAppForDeployment(ctx, e.store, app, dep)
		if err != nil {
			return fmt.Errorf("sched: warm pool: workload settings: %w", err)
		}
	}
	warm, obsolete, err := e.environmentWarmPoolInstances(ctx, app, scope, dep.ID, instances)
	if err != nil {
		return err
	}
	desired := app.WarmPoolSize
	if desired < 0 {
		desired = 0
	}
	// A missing stage release cannot borrow the shared App's production target.
	// Only proven orphaned owners enter obsolete without a selected deployment.
	if depErr != nil && !reaperProductionScope(scope) && app.Status == state.AppActive && acct.Active() && api.Plan(acct.Plan).WarmPoolAllowed() {
		return e.reclaimWarmPool(ctx, obsolete, 0)
	}
	if desired == 0 || app.Status != state.AppActive || !acct.Active() || !api.Plan(acct.Plan).WarmPoolAllowed() ||
		!instanceModeUsesSnapshots(instanceModeForApp(app)) {
		return e.reclaimWarmPool(ctx, append(warm, obsolete...), 0)
	}
	if depErr != nil {
		// A missing release cannot replace a retained, still-owned pool.
		return e.reclaimWarmPool(ctx, obsolete, 0)
	}
	values, err := e.loadRuntimeDeploymentValues(ctx, app, dep)
	if err != nil {
		return fmt.Errorf("sched: warm pool: owned runtime values: %w", err)
	}
	if securityQuarantineErr(dep) != nil || runtimeValuesHaveEphemeralSecrets(values.Snapshot) {
		return e.reclaimWarmPool(ctx, append(warm, obsolete...), 0)
	}
	var freshWarm []state.Instance
	for _, instance := range warm {
		proof, readErr := e.store.InstanceRuntimeConfigFence(ctx, app.AccountID, app.ID, instance.ID)
		if readErr != nil && !errors.Is(readErr, state.ErrNotFound) {
			return fmt.Errorf("sched: warm pool: read captured config: %w", readErr)
		}
		if readErr == nil && proof == values.ConfigFence {
			freshWarm = append(freshWarm, instance)
		} else {
			obsolete = append(obsolete, instance)
		}
	}
	warm = freshWarm
	if err := e.reclaimWarmPool(ctx, obsolete, 0); err != nil {
		return err
	}
	if len(warm) > desired {
		if err := e.reclaimWarmPool(ctx, warm, desired); err != nil {
			return err
		}
		instances, err = e.store.ListInstancesForApp(ctx, appID)
		if err != nil {
			return fmt.Errorf("sched: warm pool: refresh instances: %w", err)
		}
		warm, _, err = e.environmentWarmPoolInstances(ctx, app, scope, dep.ID, instances)
		if err != nil {
			return err
		}
	}
	if len(warm) >= desired || *budget <= 0 {
		return nil
	}

	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return fmt.Errorf("sched: warm pool: unknown plan %q", acct.Plan)
	}
	snap, haveSnap, _ := e.usableSnapshotForWake(ctx, dep.ID, string(acct.Plan), app.RAMMB, app.AppProtocol)
	if !haveSnap || snap.StorageKey == "" {
		// A first deployment may not have completed snapshot prime yet.
		// Warm capacity is best-effort cache, so leave the target pending.
		return nil
	}
	paused, ok := e.vmm.(PausedRestoreVMM)
	if !ok {
		return fmt.Errorf("sched: warm pool: vmmd does not support paused restore")
	}

	toCreate := desired - len(warm)
	if toCreate > *budget {
		toCreate = *budget
	}
	for i := 0; i < toCreate; i++ {
		*budget -= 1
		if err := e.restoreWarmInstance(ctx, app, acct, limits, dep, snap, paused); err != nil {
			// Capacity is a normal convergence outcome; the next pass may
			// place on a different node after serving traffic drains.
			e.log.Warn("sched: warm pool: restore paused instance", "app", appID, "err", err)
			break
		}
	}
	return nil
}

// environmentWarmPoolInstances retains each physical deployment's original
// lifetime. Unknown metadata fails closed; a proven lost owner is obsolete and
// cannot satisfy the target of a stage recreated under the same slug.
func (e *Engine) environmentWarmPoolInstances(ctx context.Context, app state.App, scope, deploymentID string, instances []state.Instance) ([]state.Instance, []state.Instance, error) {
	var current, obsolete []state.Instance
	deployments := map[string]state.Deployment{}
	owners := map[string]error{}
	for _, instance := range instances {
		if state.State(instance.State) != state.StateWarm {
			continue
		}
		if instance.AppID != app.ID || instance.DeploymentID == "" {
			return nil, nil, fmt.Errorf("sched: warm pool: invalid paused instance owner: %w", state.ErrConflict)
		}
		dep, ok := deployments[instance.DeploymentID]
		if !ok {
			var err error
			dep, err = e.store.DeploymentByID(ctx, instance.DeploymentID)
			if err != nil {
				return nil, nil, fmt.Errorf("sched: warm pool: paused deployment owner: %w", err)
			}
			deployments[instance.DeploymentID] = dep
		}
		if dep.AppID != app.ID {
			return nil, nil, fmt.Errorf("sched: warm pool: paused app owner: %w", state.ErrConflict)
		}
		depScope := normalizedDeploymentScope(dep.Scope)
		if depScope != scope && (!reaperProductionScope(depScope) || !reaperProductionScope(scope)) {
			continue
		}
		ownerErr, checked := owners[dep.ID]
		if !checked {
			_, ownerErr = e.runtimeScalingStateForDeployment(ctx, app, dep)
			owners[dep.ID] = ownerErr
		}
		if ownerErr != nil && !errors.Is(ownerErr, state.ErrNotFound) {
			return nil, nil, fmt.Errorf("sched: warm pool: original paused owner: %w", ownerErr)
		}
		if ownerErr == nil && (deploymentID == "" || (dep.ID == deploymentID && instance.RAMMB == app.RAMMB && instanceModeMatchesApp(app, instance))) {
			current = append(current, instance)
		} else {
			obsolete = append(obsolete, instance)
		}
	}
	return current, obsolete, nil
}

// refreshWarmPoolSizeGauge projects durable resident WARM rows into the
// plan-labelled operator gauge. It deliberately counts rows rather than the
// configured target: capacity pressure, stale cleanup, and a just-completed
// promotion are visible as the actual resident pool size.
func (e *Engine) refreshWarmPoolSizeGauge(ctx context.Context, plan api.Plan, appID string) {
	if e == nil || e.store == nil || e.ops == nil || appID == "" {
		return
	}
	instances, err := e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		if e.log != nil {
			e.log.Warn("sched: warm pool: refresh size gauge", "app", appID, "err", err)
		}
		return
	}
	count := 0
	for _, ins := range instances {
		if state.State(ins.State) == state.StateWarm {
			count++
		}
	}
	e.ops.WarmPoolSize(plan).Set(float64(count))
}

func (e *Engine) setWarmPoolSizeGauge(plan api.Plan, count int) {
	if e == nil || e.ops == nil {
		return
	}
	if count < 0 {
		count = 0
	}
	e.ops.WarmPoolSize(plan).Set(float64(count))
}

func (e *Engine) reclaimWarmPool(ctx context.Context, warm []state.Instance, desired int) error {
	if len(warm) <= desired {
		return nil
	}
	sort.SliceStable(warm, func(i, j int) bool {
		if warm[i].StartedAt.Equal(warm[j].StartedAt) {
			return warm[i].ID < warm[j].ID
		}
		return warm[i].StartedAt.Before(warm[j].StartedAt)
	})
	var firstErr error
	for _, ins := range warm[:len(warm)-desired] {
		if err := e.Park(ctx, ins.ID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (e *Engine) restoreWarmInstance(ctx context.Context, app state.App, acct state.Account, limits api.Limits, dep state.Deployment, snap state.Snapshot, paused PausedRestoreVMM) error {
	warmHint := ""
	if e.warmAffinity != nil {
		warmHint, _ = e.warmAffinity.LastWarmNode(app.ID)
	}
	placement, err := e.choosePlacementLocked(ctx, Request{
		AppID: app.ID, Plan: acct.Plan, RAMMB: app.RAMMB, VCPU: limits.VCPU, CPUMillicores: effectiveAppCPUMillicores(app),
		MaxConcurrency: app.MaxConcurrency, PreferredNodeID: warmHint,
	})
	if err != nil {
		return err
	}
	wakeID := uuid.NewString()
	ins, err := e.store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateWaking), app.RAMMB, placement.NodeID, wakeID, instanceModeForApp(app))
	if err != nil {
		// ADR-193: a warm-pool fill must never outrank a customer wake for
		// node RAM. A durable refusal returns the typed capacity Problem so
		// the caller drops this fill attempt instead of retrying into a
		// full node.
		if capErr := e.nodeCapacityProblem(err); errors.Is(err, state.ErrNodeCapacity) {
			return capErr
		}
		return fmt.Errorf("create row: %w", err)
	}
	cleanup := func(reason string) {
		e.ledger.Release(ins.ID)
		if destroyErr := e.timedDestroy(context.WithoutCancel(ctx), placement.NodeID, ins.ID, DestroyTimeout); destroyErr != nil {
			e.log.Warn("sched: warm pool: destroy failed restore", "instance", ins.ID, "err", destroyErr)
		}
		if current, err := e.store.InstanceByID(context.WithoutCancel(ctx), ins.ID); err == nil &&
			current.State == ins.State && current.WakeID == ins.WakeID && current.NodeID == ins.NodeID {
			if err := updateInstanceStateCAS(context.WithoutCancel(ctx), e.store, ins.ID, ins.State, string(state.StateStopped)); err == nil {
				e.releaseHostPortLeases(context.WithoutCancel(ctx), ins.NodeID, ins.ID)
				e.recordCommittedInstanceTransition(context.WithoutCancel(ctx), current, state.StateWaking, state.StateStopped, app.ID, "warm_pool_restore_failed", reason)
			}
		}
	}
	if err := e.acquireHostPortLeases(ctx, placement.NodeID, ins.ID, hostPortRequestsForManifest(app.Manifest)); err != nil {
		_ = e.store.DeleteInstance(ctx, ins.ID)
		return fmt.Errorf("acquire host ports: %w", err)
	}
	e.emitInstanceChanged(ctx, ins.ID, app.ID, state.StateWaking, wakeID)
	owner, err := e.runtimeScalingStateForDeployment(ctx, app, dep)
	if err != nil {
		cleanup("original_environment_unavailable")
		return fmt.Errorf("warm pool: original environment: %w", err)
	}
	if err := e.ledger.Admit(Request{
		Instance: ins.ID, AppID: app.ID, DeploymentID: dep.ID, DeploymentScope: dep.Scope, Plan: acct.Plan,
		EnvironmentKey:        runtimeEnvironmentAdmissionKey(owner.Scope, owner.EnvironmentID),
		ProductionEnvironment: reaperProductionScope(owner.Scope),
		RAMMB:                 app.RAMMB, VCPU: limits.VCPU, CPUMillicores: effectiveAppCPUMillicores(app), MaxConcurrency: app.MaxConcurrency,
		NodeID: placement.NodeID, NodeCeilingMB: placement.CeilingMB,
		VCPUBudget: placement.VCPUBudget, CPUBudgetMillicores: placement.CPUBudgetMillicores, Kind: KindWarmPool,
	}); err != nil {
		e.releaseHostPortLeases(ctx, placement.NodeID, ins.ID)
		_ = e.store.DeleteInstance(ctx, ins.ID)
		return err
	}
	spec, values, err := e.buildAppSpecForMigrationWithValues(ctx, ins.ID)
	if err != nil {
		cleanup("build_spec_failed")
		return err
	}
	if runtimeValuesHaveEphemeralSecrets(values) {
		cleanup("ephemeral_secret")
		return fmt.Errorf("warm restore selected ephemeral secrets: %w", state.ErrConflict)
	}
	if int(spec.MemSizeMiB) != ins.RAMMB || int(spec.CPUMillicores) != effectiveAppCPUMillicores(app) {
		cleanup("resource_shape_changed")
		return fmt.Errorf("warm restore resource shape changed: %w", state.ErrConflict)
	}
	vmstatePath, vmstateStorageKey := e.snapshotStateLocators(placement.NodeID, snap)
	restoreCtx, cancel := context.WithTimeout(ctx, e.budgetForWake(bootInput{haveSnap: true, snapKey: snap.StorageKey}))
	out, err := paused.CreatePausedFromSnapshot(restoreCtx, placement.NodeID, ins.ID, spec, SnapshotRef{
		DeploymentID: dep.ID, FCVersion: snap.FCVersion, StorageKey: snap.StorageKey,
		VMStatePath: vmstatePath, VMStateStorageKey: vmstateStorageKey,
	})
	cancel()
	if err != nil {
		cleanup("paused_restore_failed")
		return err
	}
	if out == nil {
		cleanup("paused_restore_empty")
		return errors.New("vmmd returned an empty paused restore outcome")
	}
	current, err := e.store.RuntimeAppValuesForDeployment(ctx, acct.ID, app.ID, dep.ID)
	if err != nil || !sameRuntimeValuesSnapshot(values, current) {
		cleanup("runtime_values_changed")
		if err != nil {
			return fmt.Errorf("warm restore runtime owner changed: %w", err)
		}
		return fmt.Errorf("warm restore runtime values changed: %w", state.ErrConflict)
	}
	fence, err := state.NewRuntimeAppConfigFence(values)
	if err != nil {
		cleanup("runtime_fence_failed")
		return err
	}
	fresh, err := e.store.PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{
		AccountID: acct.ID, AppID: app.ID, InstanceID: ins.ID, NodeID: ins.NodeID, WakeID: ins.WakeID,
		ExpectedState: string(state.StateWaking), TargetState: string(state.StateWarm), Fence: fence.SecretFence, ConfigFence: fence,
		Netns: out.Netns, HostIP: out.HostIP, GuestUID: int(out.LeaseUID),
	})
	if err != nil {
		cleanup("publish_warm_state_failed")
		return err
	}
	e.recordCommittedInstanceTransition(ctx, fresh, state.StateWaking, state.StateWarm, app.ID, "warm_pool_restore", "")
	return nil
}

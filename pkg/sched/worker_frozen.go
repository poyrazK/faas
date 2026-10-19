package sched

// adr: 20261019103000 — frozen worker pools.

// Frozen workers (ADR-20261019103000) extend the frozen tier (ADR-1151) to
// declared workers. A pool scaling in freezes a surplus worker that holds no
// queue lease instead of stopping it; a pool scaling out thaws its frozen
// workers before it boots new ones. Workers never restore from a snapshot
// (ADR-138), so a frozen worker that expires, fails to thaw or loses its
// node stops, and the next demand cold-boots as before.

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// EnableFrozenTierWorkers lets worker pools freeze surplus workers (schedd
// `frozen_tier_workers`, off by default). It has no effect unless the frozen
// tier itself is enabled. Frozen workers thaw and stop either way.
func (e *Engine) EnableFrozenTierWorkers() { e.frozenTierWorkers.Store(true) }

// workerFreezeRefusal names why a surplus RUNNING worker must stop instead of
// freezing, or returns "" when it may freeze. "disabled" is not observed.
func (e *Engine) workerFreezeRefusal(ctx context.Context, app state.App, ins state.Instance, leases map[string]int) (string, error) {
	if !e.frozenTierEnabled() || !e.frozenTierWorkers.Load() {
		return "disabled", nil
	}
	if _, ok := e.vmm.(FrozenVMM); !ok {
		return "disabled", nil
	}
	// A frozen worker cannot settle a lease it holds; it would expire and be
	// redelivered after its visibility timeout.
	if leases[ins.ID] > 0 {
		return "busy", nil
	}
	if app.Status != state.AppActive || e.privateNetworkProjection(ctx, app).NetworkID != "" {
		return "ineligible", nil
	}
	if ins.RAMMB != app.RAMMB || e.runtimeConfigStale(ctx, ins) {
		return "ineligible", nil
	}
	if app.Manifest.Profiling != nil && app.Manifest.Profiling.Enabled {
		return "ineligible", nil
	}
	if ephemeral, err := e.hasEphemeralSecretForInstance(ctx, ins, app); err != nil || ephemeral {
		return "ineligible", err
	}
	// A worker that takes push deliveries listens for them, and the push
	// path does not thaw a frozen worker.
	bindings, err := e.store.ListQueueBindingHistoryForApp(ctx, app.AccountID, app.ID)
	if err != nil {
		return "", err
	}
	for _, binding := range bindings {
		if binding.Enabled && binding.RetiredAt == nil && binding.Mode == "push" {
			return "ineligible", nil
		}
	}
	if e.ledger.FrozenCountForNode(ins.NodeID) >= api.FrozenMaxPerNode {
		return "node_full", nil
	}
	return "", nil
}

// freezeSurplusWorker freezes a worker the pool no longer needs. It reports
// false when the worker must stop instead.
func (e *Engine) freezeSurplusWorker(ctx context.Context, app state.App, ins state.Instance, leases map[string]int) bool {
	reason, err := e.workerFreezeRefusal(ctx, app, ins, leases)
	if err != nil {
		e.log.Warn("sched: worker freeze: eligibility", "instance", ins.ID, "err", err)
		return false
	}
	if reason != "" {
		if reason != "disabled" {
			e.observeFreeze(reason)
		}
		return false
	}
	freezer := e.vmm.(FrozenVMM)
	locked, err := e.lockedRunning(ctx, ins.ID)
	if err != nil || locked == nil {
		return false
	}
	defer e.unlockApp(locked.AppID)
	if err := updateInstanceStateCAS(ctx, e.store, locked.ID, string(state.StateRunning), string(state.StateFrozen)); err != nil {
		return false
	}
	e.recordCommittedInstanceTransition(ctx, *locked, state.StateRunning, state.StateFrozen, locked.AppID, "worker_freeze", "")
	freezeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), frozenRPCTimeout)
	_, freezeErr := freezer.FreezeInstance(freezeCtx, e.nodeForRoute(locked.NodeID), locked.ID, false)
	cancel()
	if freezeErr != nil {
		if casErr := updateInstanceStateCAS(context.WithoutCancel(ctx), e.store, locked.ID, string(state.StateFrozen), string(state.StateRunning)); casErr != nil {
			e.log.Warn("sched: worker freeze: restore RUNNING after failed freeze", "instance", locked.ID, "err", casErr)
		} else {
			e.recordCommittedInstanceTransition(ctx, *locked, state.StateFrozen, state.StateRunning, locked.AppID, "worker_freeze_failed", problemCode(freezeErr))
		}
		e.observeFreeze("error")
		e.log.Warn("sched: worker freeze failed; stopping instead", "instance", locked.ID, "err", freezeErr)
		return false
	}
	e.ledger.Freeze(locked.ID, frozenChargeMB(locked.RAMMB, 0))
	// FrozenTTL counts from last_request_at, which a worker never sets.
	touch := []state.InstanceTouch{{InstanceID: locked.ID, LastRequest: e.now()}}
	if _, err := e.store.TouchInstancesLastSeen(context.WithoutCancel(ctx), touch); err != nil {
		e.log.Warn("sched: worker freeze: stamp freeze time", "instance", locked.ID, "err", err)
	}
	e.observeFreeze("frozen")
	return true
}

// stopFrozenWorkersForSlots stops up to needed frozen workers of the
// account's other pools, the longest frozen first, when this pool's demand
// is capped by WorkerReplicasMax. A frozen worker is still resident, and the
// store's worker reservation counts every resident worker, so it gives its
// slot back by stopping. It reports whether any stopped.
func (e *Engine) stopFrozenWorkersForSlots(ctx context.Context, app state.App, current []state.Instance, needed int) bool {
	account, err := state.AppResourceAccount(ctx, e.store, app)
	if err != nil {
		return false
	}
	all, err := e.store.ListInstancesForAccount(ctx, account.ResourceOwnerID())
	if err != nil {
		e.log.Warn("sched: worker slots: list account workers", "app", app.ID, "err", err)
		return false
	}
	own := make(map[string]bool, len(current))
	for _, ins := range current {
		own[ins.ID] = true
	}
	var frozen []state.Instance
	for _, ins := range all {
		if ins.Mode == string(state.InstanceModeWorker) && state.State(ins.State) == state.StateFrozen && !own[ins.ID] {
			frozen = append(frozen, ins)
		}
	}
	sort.SliceStable(frozen, func(i, j int) bool { return frozen[i].LastRequestAt.Before(frozen[j].LastRequestAt) })
	stopped := 0
	for _, ins := range frozen {
		if stopped >= needed {
			break
		}
		if err := e.ParkFrozen(ctx, ins.ID, "slot_needed"); err != nil {
			e.log.Warn("sched: worker slots: stop frozen worker", "instance", ins.ID, "err", err)
			continue
		}
		stopped++
	}
	return stopped > 0
}

// thawFrozenWorker resumes one of the pool's frozen workers instead of
// booting a new one. It reports false when the worker did not thaw; a worker
// that could not thaw has stopped, and the pool admits a fresh one.
func (e *Engine) thawFrozenWorker(ctx context.Context, app state.App, deploymentID string, ins state.Instance) bool {
	thawer, ok := e.vmm.(FrozenVMM)
	if !ok {
		if err := e.ParkFrozen(ctx, ins.ID, "ineligible"); err != nil {
			e.log.Warn("sched: worker thaw: stop frozen worker", "instance", ins.ID, "err", err)
		}
		return false
	}
	frozen, err := e.lockedInStates(ctx, ins.ID, state.StateFrozen)
	if err != nil || frozen == nil {
		return false
	}
	defer e.unlockApp(frozen.AppID)
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil || dep.Status != state.DeployLive || frozen.DeploymentID != dep.ID {
		return false
	}
	acct, err := state.AppResourceAccount(ctx, e.store, app)
	if err != nil {
		e.log.Warn("sched: worker thaw: account", "instance", frozen.ID, "err", err)
		return false
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return false
	}
	values, err := e.loadRuntimeDeploymentValues(ctx, app, dep)
	if err != nil {
		e.log.Warn("sched: worker thaw: runtime values", "instance", frozen.ID, "err", err)
		return false
	}
	if runtimeValuesHaveEphemeralSecrets(values.Snapshot) {
		e.parkFrozenLocked(ctx, *frozen, "ineligible")
		return false
	}
	candidate := e.frozenCandidateNode(ctx, frozen.NodeID)
	if candidate.nodeMissing || candidate.haveNode && e.frozenNodeRowUnreachable(candidate.node) {
		e.parkFrozenLocked(ctx, *frozen, "node_restart")
		return false
	}
	candidate.ins = *frozen
	started := e.now()
	_, thawed, err := e.thawFrozenLocked(ctx, thawer, app, acct, limits, dep, values, candidate, thawTiming{wakeStarted: started, thawTried: started})
	if err != nil {
		e.log.Warn("sched: worker thaw", "instance", frozen.ID, "err", err)
	}
	return thawed
}

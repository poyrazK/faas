package sched

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// promoteFrozenInstanceLocked thaws one frozen VM of the deployment for a
// request (ADR-1151). The caller holds the app lock, so two wakes cannot
// claim the same row. It re-admits the full reservation like a wake; a VM
// that cannot be re-admitted, fails to thaw or no longer matches the app's
// config is parked and the request takes the ordinary wake path. It runs
// whether or not the frozen tier is enabled, so turning it off drains the
// tier through requests as well as the TTL.
func (e *Engine) promoteFrozenInstanceLocked(ctx context.Context, app state.App, acct state.Account, limits api.Limits, dep state.Deployment, mode string) (WakeResult, bool, error) {
	// The ledger knows which apps have frozen VMs, so an app without one
	// costs no instance listing on the wake path.
	if app.Status != state.AppActive || !instanceModeUsesSnapshots(mode) || e.ledger.FrozenCountForApp(app.ID) == 0 {
		return WakeResult{}, false, nil
	}
	candidates, err := e.frozenCandidates(ctx, app, dep)
	if err != nil || len(candidates) == 0 {
		return WakeResult{}, false, err
	}
	values, err := e.loadRuntimeDeploymentValues(ctx, app, dep)
	if err != nil {
		return WakeResult{}, false, fmt.Errorf("sched: frozen thaw: runtime values: %w", err)
	}
	environmentKey := runtimeEnvironmentAdmissionKey(values.Snapshot.Scope, values.Snapshot.EnvironmentID)
	production := reaperProductionScope(values.Snapshot.Scope)
	if _, _, refused := e.wakeServingCapacity(app, limits, dep.ID, environmentKey, production).refusal(); refused {
		// The ordinary admission gate owns the capacity result.
		return WakeResult{}, false, nil
	}
	thawer, ok := e.vmm.(FrozenVMM)
	if !ok || runtimeValuesHaveEphemeralSecrets(values.Snapshot) {
		for _, frozen := range candidates {
			e.parkFrozenLocked(ctx, frozen, "ineligible")
		}
		return WakeResult{}, false, nil
	}
	for _, frozen := range candidates {
		result, thawed, err := e.thawFrozenLocked(ctx, thawer, app, acct, limits, dep, values, frozen)
		if err != nil {
			return WakeResult{}, false, err
		}
		if thawed {
			return result, true, nil
		}
	}
	return WakeResult{}, false, nil
}

// frozenCandidates lists the deployment's frozen rows, newest first. Rows
// whose shape no longer matches the app are parked.
func (e *Engine) frozenCandidates(ctx context.Context, app state.App, dep state.Deployment) ([]state.Instance, error) {
	instances, err := e.store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		return nil, fmt.Errorf("sched: frozen thaw: list candidates: %w", err)
	}
	var out []state.Instance
	for _, ins := range instances {
		if state.State(ins.State) != state.StateFrozen || ins.DeploymentID != dep.ID {
			continue
		}
		if ins.RAMMB != app.RAMMB || !instanceModeMatchesApp(app, ins) || ins.Netns == "" || ins.HostIP == "" {
			e.parkFrozenLocked(ctx, ins, "stale")
			continue
		}
		if e.frozenNodeUnreachable(ctx, ins.NodeID) {
			e.parkFrozenLocked(ctx, ins, "node_restart")
			continue
		}
		out = append(out, ins)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastRequestAt.Equal(out[j].LastRequestAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].LastRequestAt.After(out[j].LastRequestAt)
	})
	return out, nil
}

// thawFrozenLocked thaws one candidate. thawed=false means the candidate was
// parked and the caller may try the next one.
func (e *Engine) thawFrozenLocked(ctx context.Context, thawer FrozenVMM, app state.App, acct state.Account, limits api.Limits, dep state.Deployment, values runtimeDeploymentValues, frozen state.Instance) (WakeResult, bool, error) {
	captured, proofErr := e.store.InstanceRuntimeConfigFence(ctx, acct.ID, app.ID, frozen.ID)
	if proofErr != nil && !errors.Is(proofErr, state.ErrNotFound) {
		return WakeResult{}, false, fmt.Errorf("sched: frozen thaw: captured config: %w", proofErr)
	}
	if proofErr != nil || captured != values.ConfigFence {
		e.parkFrozenLocked(ctx, frozen, "config_changed")
		return WakeResult{}, false, nil
	}
	// Re-admit the full reservation on the VM's node, as a wake would.
	ceiling, vcpuBudget, ceilingErr := e.resolveNodeCeiling(ctx, frozen.NodeID)
	if ceilingErr != nil {
		e.log.Warn("sched: frozen thaw: node ceiling", "instance", frozen.ID, "err", ceilingErr)
	}
	e.ledger.Release(frozen.ID)
	if err := e.ledger.Admit(Request{
		Instance: frozen.ID, AppID: app.ID, DeploymentID: dep.ID, DeploymentScope: dep.Scope, Plan: acct.Plan,
		EnvironmentKey:        runtimeEnvironmentAdmissionKey(values.Snapshot.Scope, values.Snapshot.EnvironmentID),
		ProductionEnvironment: reaperProductionScope(values.Snapshot.Scope),
		RAMMB:                 app.RAMMB, VCPU: limits.VCPU, CPUMillicores: effectiveAppCPUMillicores(app), MaxConcurrency: app.MaxConcurrency,
		NodeID: frozen.NodeID, NodeCeilingMB: ceiling, VCPUBudget: vcpuBudget,
		CPUBudgetMillicores: e.resolveNodeCPUBudgetMillicores(ctx, frozen.NodeID),
	}); err != nil {
		e.parkFrozenLocked(ctx, frozen, "no_capacity")
		e.observeThaw("no_capacity")
		return WakeResult{}, false, nil
	}
	started := time.Now()
	thawCtx, cancel := context.WithTimeout(ctx, frozenRPCTimeout)
	healthcheckRequired := imageHealthcheckRequiredFromDep(dep)
	verified, thawErr := thawer.ThawInstance(thawCtx, e.nodeForRoute(frozen.NodeID), frozen.ID, healthcheckRequired)
	cancel()
	if thawErr == nil && healthcheckRequired && !verified {
		// The deployment requires an image healthcheck the thaw did not
		// confirm; never route to it.
		thawErr = errors.New("thaw did not verify the required image healthcheck")
	}
	if thawErr != nil {
		e.log.Warn("sched: frozen thaw failed; parking", "instance", frozen.ID, "err", thawErr)
		e.parkFrozenLocked(ctx, frozen, "thaw_failed")
		e.observeThaw("failed")
		return WakeResult{}, false, nil
	}
	fresh, publishErr := e.store.PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{
		AccountID: acct.ID, AppID: app.ID, InstanceID: frozen.ID, NodeID: frozen.NodeID, WakeID: frozen.WakeID,
		ExpectedState: string(state.StateFrozen), Fence: captured.SecretFence, ConfigFence: captured,
		Netns: frozen.Netns, HostIP: frozen.HostIP, GuestUID: frozen.GuestUID,
	})
	if publishErr != nil {
		e.log.Warn("sched: frozen thaw: publish RUNNING", "instance", frozen.ID, "err", publishErr)
		if current, loadErr := e.store.InstanceByID(ctx, frozen.ID); loadErr == nil && state.State(current.State) == state.StateFrozen {
			e.parkFrozenLocked(ctx, frozen, "publish_failed")
		} else {
			e.ledger.Release(frozen.ID)
		}
		e.observeThaw("failed")
		return WakeResult{}, false, nil
	}
	e.recordCommittedInstanceTransition(ctx, fresh, state.StateFrozen, state.StateRunning, app.ID, "frozen_thaw", "")
	e.clearSnapshotBackoffAfterWake(ctx, dep.ID)
	e.observeThaw("success")
	e.log.Debug("sched: thawed frozen instance", "instance", fresh.ID, "duration", time.Since(started))
	region := ""
	if node, nodeErr := e.store.ComputeNodeByID(ctx, fresh.NodeID); nodeErr == nil {
		region = stringValue(node.Region)
	}
	// A thaw answers the gateway as a restore, as a warm-pool resume does.
	return WakeResult{
		InstanceID: fresh.ID, NodeID: fresh.NodeID, Method: vmmdpb.WakeMethod_WAKE_RESTORE,
		WakeID: fresh.WakeID, Port: deploymentRuntimePort(dep), DeploymentID: dep.ID,
		RequestCount: fresh.RequestCount, Identity: platformIdentity(app, dep, acct, fresh.NodeID, fresh.ID, region),
	}, true, nil
}

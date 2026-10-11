package sched

// adr: 755
import (
	"context"
	"errors"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// An uncertain regional promotion owns the request result. Never advance to
// another warm candidate or a cold launch, and never destroy/release the hold.
func (e *Engine) promoteRegionalWarmInstance(ctx context.Context, app state.App, acct state.Account, dep state.Deployment, warm state.Instance, grant runtimequota.Grant) (WakeResult, error) {
	if err := checkRegionalRuntimeQuota(ctx, e.store, app.ID); err != nil {
		return WakeResult{}, err
	}
	if _, ok := e.vmm.(RegionalWarmResumeVMM); !ok || warm.Netns == "" || warm.HostIP == "" {
		return WakeResult{}, regionalWarmUnavailable(runtimequota.ErrBinding)
	}
	values, err := e.loadRuntimeDeploymentValues(ctx, app, dep)
	if err != nil || runtimeValuesHaveEphemeralSecrets(values.Snapshot) {
		return WakeResult{}, regionalWarmUnavailable(errors.Join(err, state.ErrConflict))
	}
	captured, err := e.store.InstanceRuntimeConfigFence(ctx, acct.ID, app.ID, warm.ID)
	if err != nil || captured != values.ConfigFence {
		return WakeResult{}, regionalWarmUnavailable(errors.Join(err, state.ErrConflict))
	}
	if err := e.repairRegionalWarmLedger(ctx, app, acct, dep, warm); err != nil {
		return WakeResult{}, regionalWarmUnavailable(err)
	}
	p, err := e.prepareRegionalWarmResume(ctx, app, acct, dep, warm, grant)
	if err != nil {
		return WakeResult{}, regionalWarmUnavailable(err)
	}
	if err := checkRegionalRuntimeQuota(ctx, e.store, app.ID); err != nil {
		return WakeResult{}, err
	}
	started := time.Now()
	resumeCtx, cancel := context.WithTimeout(ctx, e.budgetForWake(bootInput{haveSnap: true, snapKey: "warm_pool"}))
	err = e.dispatchRegionalWarmResume(resumeCtx, warm, p, imageHealthcheckRequiredFromDep(dep))
	cancel()
	if err != nil {
		return WakeResult{}, regionalWarmUnavailable(err)
	}
	if err := checkRegionalRuntimeQuota(ctx, e.store, app.ID); err != nil {
		return WakeResult{}, err
	}
	fresh, committed, err := e.publishRegionalWarmRuntime(ctx, acct.ID, warm, p, captured)
	if err != nil {
		return WakeResult{}, regionalWarmUnavailable(err)
	}
	if committed {
		e.recordCommittedInstanceTransition(ctx, fresh, state.StateWarm, state.StateRunning, app.ID, "warm_pool_resume", "")
	}
	if err := checkRegionalRuntimeQuota(ctx, e.store, app.ID); err != nil {
		return WakeResult{}, err
	}
	e.clearSnapshotBackoffAfterWake(ctx, dep.ID)
	e.observeWarmResumeDuration(app.ID, warm.WakeID, time.Since(started))
	e.observeWarmResume("success")
	return WakeResult{InstanceID: fresh.ID, NodeID: fresh.NodeID, Method: vmmdpb.WakeMethod_WAKE_RESTORE, WakeID: fresh.WakeID, Port: deploymentRuntimePort(dep), DeploymentID: dep.ID, RequestCount: fresh.RequestCount, Identity: platformIdentity(app, dep, acct, fresh.NodeID, fresh.ID, grant.Binding.Region)}, nil
}

func (e *Engine) repairRegionalWarmLedger(ctx context.Context, app state.App, acct state.Account, dep state.Deployment, warm state.Instance) error {
	if e.ledger.ResidentFor(warm.ID) {
		return nil
	}
	ceiling, vcpu, err := e.resolveNodeCeiling(ctx, warm.NodeID)
	if err != nil {
		return err
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return runtimequota.ErrBinding
	}
	sidecarRAM, err := deploymentSidecarRAM(dep)
	if err != nil {
		return err
	}
	owner, err := e.runtimeScalingStateForDeployment(ctx, app, dep)
	if err != nil {
		return err
	}
	return e.ledger.Admit(Request{Instance: warm.ID, AppID: app.ID, DeploymentID: dep.ID, DeploymentScope: dep.Scope, Plan: acct.Plan, RuntimeRunID: warm.WakeID, EnvironmentKey: runtimeEnvironmentAdmissionKey(owner.Scope, owner.EnvironmentID), ProductionEnvironment: reaperProductionScope(owner.Scope), RAMMB: warm.RAMMB, SidecarMBs: sidecarRAM, rowShadow: true, VCPU: limits.VCPU, CPUMillicores: effectiveAppCPUMillicores(app), MaxConcurrency: app.MaxConcurrency, NodeID: warm.NodeID, NodeCeilingMB: ceiling, VCPUBudget: vcpu, CPUBudgetMillicores: e.resolveNodeCPUBudgetMillicores(ctx, warm.NodeID), Kind: KindWarmPool})
}

// A peer may have published the same acknowledged resident. Recover only its
// exact runtime identity and still-issued placement; no conflict frees capacity.
func (e *Engine) publishRegionalWarmRuntime(ctx context.Context, accountID string, warm state.Instance, p runtimequota.Placement, captured state.RuntimeAppConfigFence) (state.Instance, bool, error) {
	if !regionalWarmPlacementMatches(warm, p) || !runtimequota.MatchesID(p.Permit.Grant.Binding.RegionalAccountID, accountID) {
		return state.Instance{}, false, runtimequota.ErrBinding
	}
	if err := e.store.CheckRegionalRuntimeIssuedPlacement(ctx, p); err != nil {
		return state.Instance{}, false, err
	}
	fresh, err := e.store.PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{
		AccountID: accountID, AppID: warm.AppID, InstanceID: warm.ID, NodeID: warm.NodeID, WakeID: warm.WakeID,
		ExpectedState: string(state.StateWarm), Fence: captured.SecretFence, ConfigFence: captured,
		Netns: warm.Netns, HostIP: warm.HostIP, GuestUID: warm.GuestUID,
	})
	committed := err == nil
	if errors.Is(err, state.ErrConflict) {
		fresh, err = e.store.InstanceByID(ctx, warm.ID)
	}
	if err != nil {
		return state.Instance{}, false, err
	}
	if fresh.State != string(state.StateRunning) || fresh.ID != warm.ID || fresh.AppID != warm.AppID || fresh.DeploymentID != warm.DeploymentID || fresh.NodeID != warm.NodeID || fresh.WakeID != warm.WakeID || fresh.Netns != warm.Netns || fresh.HostIP != warm.HostIP || fresh.GuestUID != warm.GuestUID {
		return state.Instance{}, false, runtimequota.ErrBinding
	}
	currentFence, fenceErr := e.store.InstanceRuntimeConfigFence(ctx, accountID, warm.AppID, warm.ID)
	if fenceErr != nil || currentFence != captured {
		return state.Instance{}, false, errors.Join(fenceErr, state.ErrConflict)
	}
	if err := e.store.CheckRegionalRuntimeIssuedPlacement(ctx, p); err != nil {
		return state.Instance{}, false, err
	}
	return fresh, committed, nil
}

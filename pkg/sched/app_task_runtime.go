package sched

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ResolveAppTaskRuntime resolves the immutable deployment pin captured when
// the task was created. It deliberately accepts AppTaskRestoreRequest, which
// has no command field, so placement, secret loading, and vmmd restore cannot
// observe customer command bytes before the durable running fence.
func (e *Engine) ResolveAppTaskRuntime(ctx context.Context, request AppTaskRestoreRequest) (ResolvedAppTaskRuntime, error) {
	if e == nil || e.store == nil || e.ledger == nil {
		return ResolvedAppTaskRuntime{}, ErrAppTaskCoordinatorNotWired
	}
	if request.ID == "" || request.AccountID == "" || request.AppID == "" || request.DeploymentID == "" {
		return ResolvedAppTaskRuntime{}, fmt.Errorf("%w: incomplete app task identity", state.ErrAppTaskInvalid)
	}
	release := e.lockApp(request.AppID)
	defer release()

	app, err := e.store.AppByID(ctx, request.AppID)
	if err != nil {
		return ResolvedAppTaskRuntime{}, fmt.Errorf("sched: resolve app task app: %w", err)
	}
	if app.AccountID != request.AccountID || app.Status == state.AppDeleted {
		return ResolvedAppTaskRuntime{}, state.ErrAppTaskDeploymentUnavailable
	}
	dep, err := e.store.DeploymentByID(ctx, request.DeploymentID)
	if err != nil {
		return ResolvedAppTaskRuntime{}, fmt.Errorf("sched: resolve app task deployment: %w", err)
	}
	if dep.AppID != app.ID || dep.RootfsKey == "" || dep.RootfsKey != request.ArtifactKey ||
		dep.ImageDigest == "" || dep.ImageDigest != request.ImageDigest ||
		normalizedDeploymentScope(dep.Scope) != normalizedDeploymentScope(request.DeploymentScope) {
		return ResolvedAppTaskRuntime{}, state.ErrAppTaskDeploymentUnavailable
	}
	app = appForDeployment(app, dep)
	acct, err := e.store.AccountByID(ctx, request.AccountID)
	if err != nil {
		return ResolvedAppTaskRuntime{}, fmt.Errorf("sched: resolve app task account: %w", err)
	}
	if acct.ID != app.AccountID {
		return ResolvedAppTaskRuntime{}, state.ErrAppTaskDeploymentUnavailable
	}
	limits := api.MustLimitsFor(acct.Plan)
	placement, err := e.choosePlacementLocked(ctx, Request{
		AppID: app.ID, Plan: acct.Plan, RAMMB: app.RAMMB, VCPU: limits.VCPU,
		CPUMillicores: effectiveAppCPUMillicores(app), MaxConcurrency: app.MaxConcurrency,
		PreferredNodeID: app.NodeID,
	})
	if err != nil {
		return ResolvedAppTaskRuntime{}, err
	}
	if err := e.verifyPrimeLayer(ctx, app.ID, request.ArtifactKey); err != nil {
		return ResolvedAppTaskRuntime{}, fmt.Errorf("sched: resolve app task artifact: %w", err)
	}
	sealedEnv, err := e.loadSealedEnvFor(ctx, app.AccountID, app.ID, dep.Scope, envSecretsFromDep(dep))
	if err != nil {
		return ResolvedAppTaskRuntime{}, fmt.Errorf("sched: resolve app task sealed env: %w", err)
	}
	privateNetwork := e.privateNetworkProjection(ctx, app)
	healthcheckGRPC, healthcheckGRPCService := healthcheckGRPCFromDep(dep)
	spec := AppSpec{
		BaseKey: baseKey(app.Runtime), LayerKey: request.ArtifactKey,
		VCPUCount: int32(limits.VCPU), MemSizeMiB: int32(app.RAMMB),
		CPUMillicores: int32(effectiveAppCPUMillicores(app)), EgressMbit: int32(limits.EgressMbit),
		StartupDeadlineS: startupDeadlineForApp(app, acct.Plan), ExecutionMode: executionModeForApp(app),
		Plan: acct.Plan, AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID,
		SealedEnv: sealedEnv,
		APIEnv: appendPlatformIdentity(e.loadAPIEnv(ctx, app.AccountID, app.ID, dep.Scope),
			app, dep, acct, placement.NodeID, request.ID, placement.Region),
		EgressAllowlist:     prefixesToCIDRStrings(app.EgressAllowlist),
		PrivateNetworkCIDRs: privateNetwork.CIDRs, PrivateNetworkAllowedCIDRs: privateNetwork.AllowedCIDRs,
		PrivateNetworkFirewallRules: privateNetwork.FirewallRules, PrivateNetworkID: privateNetwork.NetworkID,
		PrivateNetworkAddress: privateNetwork.Address, StaticEgressIP: staticEgressIPString(app.StaticEgressIP),
		Port: deploymentRuntimePort(dep), HealthcheckPath: healthcheckPathFromDep(dep),
		HealthcheckGRPC:        healthcheckGRPC,
		HealthcheckGRPCService: healthcheckGRPCService,
		Runtime:                app.Runtime, AppProtocol: app.AppProtocol,
	}
	if spec.BaseKey == "" || spec.LayerKey == "" || !spec.Plan.Valid() {
		return ResolvedAppTaskRuntime{}, errors.New("sched: app task runtime projection is incomplete")
	}
	if err := e.ledger.Admit(Request{
		Instance: request.ID, AppID: app.ID, DeploymentID: dep.ID, Plan: acct.Plan,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, CPUMillicores: effectiveAppCPUMillicores(app),
		MaxConcurrency: app.MaxConcurrency, Kind: KindAppTask, NodeID: placement.NodeID,
		NodeCeilingMB: placement.CeilingMB, VCPUBudget: placement.VCPUBudget,
		CPUBudgetMillicores: placement.CPUBudgetMillicores,
	}); err != nil {
		return ResolvedAppTaskRuntime{}, err
	}
	return ResolvedAppTaskRuntime{
		NodeID: placement.NodeID,
		Spec:   AppTaskRestoreSpec{Instance: request.ID, DeploymentID: dep.ID, App: spec},
		release: func() {
			e.ledger.Release(request.ID)
		},
	}, nil
}

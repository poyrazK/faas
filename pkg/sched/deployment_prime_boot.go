package sched

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Both ordinary priming and graph qualification materialize this boot payload.
// The caller owns admission, authority checks, VM effects and cleanup.
type deploymentPrimeBoot struct {
	Spec             AppSpec
	Inputs           state.RuntimeConfigInputs
	SecretDeliveries []state.AppSecretDeliveryCandidate
	RejectionReason  string
	SecretFence      state.RuntimeAppSecretFence
	ConfigFence      state.RuntimeAppConfigFence
}

func (e *Engine) prepareDeploymentPrimeBoot(ctx context.Context, app state.App, acct state.Account, limits api.Limits, dep state.Deployment, placement Placement, ins state.Instance) (deploymentPrimeBoot, error) {
	result := deploymentPrimeBoot{}
	appID, primeLayer := app.ID, layerKey(dep.RootfsKey, dep.ID)
	runtimeValues, err := e.loadRuntimeDeploymentValues(ctx, app, dep)
	runtimeInputs := runtimeValues.Inputs
	if err != nil {
		result.RejectionReason = "prime_runtime_inputs_invalid"
		return result, fmt.Errorf("sched: prime: load runtime inputs: %w", err)
	}
	sealedEnv := runtimeValues.MainSecrets
	sidecars, sidecarSecretCandidates, err := e.sidecarsForDeploymentWithValues(ctx, dep, acct.ID, &runtimeValues.Snapshot)
	if err != nil {
		result.RejectionReason = "prime_sealed_env_invalid"
		return result, fmt.Errorf("sched: prime: load sealed env: %w", err)
	}
	sealedEnv.Candidates, err = mergeSecretDeliveryCandidates(sealedEnv.Candidates, sidecarSecretCandidates)
	if err != nil {
		result.RejectionReason = "prime_secret_version_changed"
		return result, fmt.Errorf("sched: prime: sidecar secret versions changed during preparation: %w", err)
	}
	addRuntimeSecretVersions(&runtimeInputs, sealedEnv.Candidates, sealedEnv.AllSecrets)
	addRuntimeSidecarSecretVersions(&runtimeInputs, sidecarSecretCandidates)
	runtimeInputs.SecretRefs = sealedEnv.References
	mainDependencies, err := mainWorkloadDependenciesForDeployment(dep, sidecars)
	if err != nil {
		result.RejectionReason = "prime_main_dependencies_invalid"
		return result, fmt.Errorf("sched: prime: load primary workload dependencies: %w", err)
	}
	privateNetwork := e.privateNetworkProjection(ctx, app)
	healthcheckGRPC, healthcheckGRPCService := healthcheckGRPCFromDep(dep)
	spec := AppSpec{
		BaseKey: baseKey(app.Runtime), LayerKey: primeLayer,
		VCPUCount: int32(limits.VCPU), MemSizeMiB: int32(app.RAMMB), CPUMillicores: int32(effectiveAppCPUMillicores(app)),
		EgressMbit: int32(limits.EgressMbit),
		// M-3: deploy prime uses the same plan-resolved readiness budget
		// as ordinary wakes, so first boot and later wakes agree.
		StartupDeadlineS:       startupDeadlineForApp(app, acct.Plan),
		DisableStartupCPUBoost: dep.DisableStartupCPUBoost,
		ExecutionMode:          executionModeForApp(app),
		Plan:                   acct.Plan, AccountID: acct.ID,
		AppID: appID, DeploymentID: dep.ID,
		SealedEnv:     sealedEnv.Entries,
		Sidecars:      sidecars,
		MainDependsOn: mainDependencies,
		// Issue #395 / ADR-045: plaintext api_env layer mirrors the
		// sealed secrets surface but stores non-sensitive runtime
		// config. Precedence at the guest layer is "secrets >
		// api_env > manifest_env > os.environ".
		APIEnv: appendPlatformIdentity(
			runtimeValues.APIEnv,
			app, dep, acct, placement.NodeID, ins.ID, placement.Region,
		),
		// ADR-031: see the Wake builder above. Prime is the
		// deploy-pipeline first boot — same wire shape, same
		// per-netns ruleset; a freshly-deployed app starts under
		// its declared egress policy rather than awaiting a later
		// wake.
		EgressAllowlist:             prefixesToCIDRStrings(app.EgressAllowlist),
		EgressPorts:                 app.EgressPorts,
		PrivateNetworkCIDRs:         privateNetwork.CIDRs,
		PrivateNetworkAllowedCIDRs:  privateNetwork.AllowedCIDRs,
		PrivateNetworkFirewallRules: privateNetwork.FirewallRules,
		PrivateNetworkID:            privateNetwork.NetworkID,
		PrivateNetworkAddress:       privateNetwork.Address,
		// ADR-119: see the Wake builder above. Prime threads
		// the customer-supplied static IPv4 (BYOIP, Scale-only)
		// onto the vmmd AppSpec so the per-netns renderer
		// emits the SNAT-to-customer sibling rule.
		StaticEgressIP: staticEgressIPString(app.StaticEgressIP),
		// ADR-053: snapshot priming is the first cold boot for a source
		// deployment, so it must use the same resolved guest port as later
		// wakes. Without this field vmmd falls back to guest :8080 while the
		// inferred profile starts Node/Python apps on their framework port.
		Port:                   deploymentRuntimePort(dep),
		HealthcheckPath:        healthcheckPathFromDep(dep),
		HealthcheckGRPC:        healthcheckGRPC,
		HealthcheckGRPCService: healthcheckGRPCService,
		ReadinessProbeJSON:     string(dep.OverrideReadinessProbe),
		// Issue #470 / PR #470-FU-B: per-deployment runner id
		// (e.g. "node22"). Threaded onto the vmmd AppSpec so
		// the framework_ready DGRAM receipt path can label
		// vmmd_guest_framework_warmup_seconds by runner. See
		// buildAppSpec (engine.go:1757) for the same field
		// wired on the (re)build path. Empty falls back to
		// "unknown" in the histogram observer.
		Runtime:     app.Runtime,
		AppProtocol: app.AppProtocol,
	}
	result.Spec, result.Inputs, result.SecretDeliveries = spec, runtimeInputs, sealedEnv.Candidates
	result.SecretFence, result.ConfigFence = sealedEnv.Fence, runtimeValues.ConfigFence
	return result, nil
}

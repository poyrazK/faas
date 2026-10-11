package state

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ EnvironmentGitOpsServiceBindingStore = (*MemStore)(nil)

func (m *MemStore) ResolveEnvironmentGitOpsServiceBinding(ctx context.Context, callerAppID, callerDeploymentID, service string) (EnvironmentGitOpsServiceBindingRoute, bool, bool, error) {
	var zero EnvironmentGitOpsServiceBindingRoute
	service = strings.ToLower(strings.TrimSpace(service))
	if !qualificationRecoveryUUIDValid(callerAppID) || service == "" || !api.ValidAppSlug(service) {
		return zero, false, false, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return zero, false, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	callerApp, ok := m.apps[callerAppID]
	if !ok || callerApp.Status == AppDeleted {
		return zero, false, false, ErrNotFound
	}
	if callerDeploymentID == "" {
		// A caller without node-verified deployment identity cannot use a
		// GitOps route. Detect an active managed member so callers cannot fall
		// through to app-slug resolution when that identity is missing.
		for _, release := range m.projectReleaseSets {
			if !release.Active || release.ProjectID != callerApp.ProjectID || release.AccountID != callerApp.AccountID {
				continue
			}
			if deploymentID := releaseMemberForApp(release, callerAppID); deploymentID != "" {
				if dep, exists := m.deployments[deploymentID]; exists && dep.EnvironmentWorkloadManaged() {
					return zero, true, false, ErrConflict
				}
			}
		}
		return zero, false, false, nil
	}
	callerDeployment, exists := m.deployments[callerDeploymentID]
	if !exists || callerDeployment.AppID != callerAppID {
		return zero, false, false, ErrNotFound
	}
	if !callerDeployment.EnvironmentWorkloadManaged() {
		return zero, false, false, nil
	}
	caller, err := callerDeployment.ScopedWorkloadRuntime()
	if err != nil || caller == nil {
		return zero, true, false, ErrConflict
	}
	memory, active := m.environmentGitOps[caller.SourceID]
	if !active || memory.source.Detached || memory.source.Suspended || memory.source.Spec.Mode != "enforce" ||
		memory.source.AccountID != callerApp.AccountID || memory.source.ProjectID != callerApp.ProjectID ||
		memory.source.EnvironmentID != caller.EnvironmentID || normalizedDeploymentScope(memory.source.EnvironmentSlug) != normalizedDeploymentScope(callerDeployment.Scope) {
		return zero, true, false, ErrConflict
	}
	releaseID := m.activeProjectReleaseSets[releaseKey(callerApp.ProjectID, callerDeployment.Scope)]
	release, active := m.projectReleaseSets[releaseID]
	if !active || !release.Active || !releasePubliclyUsable(release, time.Now()) || release.AccountID != callerApp.AccountID ||
		release.ProjectID != callerApp.ProjectID || normalizedDeploymentScope(release.EnvironmentSlug) != normalizedDeploymentScope(callerDeployment.Scope) ||
		releaseMemberForApp(release, callerAppID) != callerDeploymentID || callerDeployment.Status != DeployLive || callerDeployment.EnvironmentWorkloadHeld() {
		return zero, true, false, ErrConflict
	}

	var targetAppID string
	for _, binding := range caller.ServiceBindings {
		if strings.EqualFold(binding.Workload, service) {
			if targetAppID != "" && targetAppID != binding.TargetAppID {
				return zero, true, false, ErrConflict
			}
			targetAppID = binding.TargetAppID
		}
	}
	if targetAppID == "" {
		return zero, true, false, nil
	}
	targetDeploymentID := releaseMemberForApp(release, targetAppID)
	if targetDeploymentID == "" {
		return zero, true, false, ErrConflict
	}
	targetDeployment, exists := m.deployments[targetDeploymentID]
	if !exists || targetDeployment.AppID != targetAppID || targetDeployment.Status != DeployLive || targetDeployment.EnvironmentWorkloadHeld() ||
		normalizedDeploymentScope(targetDeployment.Scope) != normalizedDeploymentScope(callerDeployment.Scope) {
		return zero, true, false, ErrConflict
	}
	targetApp, exists := m.apps[targetAppID]
	if !exists || targetApp.Status == AppDeleted || targetApp.AccountID != callerApp.AccountID || targetApp.ProjectID != callerApp.ProjectID {
		return zero, true, false, ErrConflict
	}
	target, err := targetDeployment.ScopedWorkloadRuntime()
	if err != nil || target == nil || target.Resource != "workload/"+service ||
		caller.SourceID != target.SourceID || caller.EnvironmentID != target.EnvironmentID || caller.RevisionID != target.RevisionID ||
		caller.Generation != target.Generation || caller.IntentVersion != target.IntentVersion || caller.PlanHash != target.PlanHash ||
		caller.DefinitionDigest != target.DefinitionDigest || target.WorkloadClass == WorkloadClassJob || target.WorkloadClass == WorkloadClassWorker {
		return zero, true, false, ErrConflict
	}
	requireHTTPS, callScope, reliability, err := environmentGitOpsServiceBindingPolicy(*caller, *target, service)
	if err != nil {
		return zero, true, false, err
	}
	return EnvironmentGitOpsServiceBindingRoute{
		CallerAppID: callerAppID, CallerDeploymentID: callerDeploymentID,
		TargetAppID: targetAppID, TargetDeploymentID: targetDeploymentID,
		ReleaseSetID: release.ID, AccountID: callerApp.AccountID,
		RequireHTTPS: requireHTTPS, CallScope: callScope, Reliability: reliability,
	}, true, true, nil
}

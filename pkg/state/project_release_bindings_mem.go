package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

var _ CheckedProjectReleaseStore = (*MemStore)(nil)

func (m *MemStore) ActivateProjectReleaseWithBindings(ctx context.Context, accountID, projectID, environment, expectedActiveID string, fallback []ProjectReleaseMember, ttl int, members []ProjectReleaseMember, fences []BindingPromotionFence) (ProjectReleaseSet, error) {
	ctx, err := checkedReleaseGraphContext(ctx, accountID, environment, members, fences)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	return m.publishProjectReleaseSet(ctx, accountID, projectID, environment, &expectedActiveID, fallback, ttl, members)
}

func (m *MemStore) checkCheckedReleaseGraphLocked(ctx context.Context, members map[string]string, scope string) error {
	for _, fence := range bindingReleaseFences(ctx) {
		policy := m.bindingReleasePolicyLocked(fence.AppID, scope)
		if policy.Mode != "enforce" || !releaseFenceMatchesPolicy(fence, policy) || members[fence.AppID] != fence.DeploymentID {
			return ErrBindingReleaseRequired
		}
		deployment := m.deployments[fence.DeploymentID]
		if err := m.checkBindingTrafficGuardLocked(deployment, &bindingTrafficGuard{fence: fence}); err != nil {
			return err
		}
		app := m.apps[fence.AppID]
		if app.Manifest.ExecutionMode != api.ExecutionModeService {
			continue
		}
		desired := 1
		if app.Manifest.ServiceReplicas != nil {
			desired = app.Manifest.ServiceReplicas.Desired
		}
		if desired < 1 {
			desired = 1
		}
		ready := 0
		for _, instance := range m.instances {
			if instance.AppID == app.ID && instance.DeploymentID == deployment.ID && instance.Mode == string(InstanceModeService) && State(instance.State) == StateRunning {
				ready++
			}
		}
		if ready < desired {
			return ErrServiceRolloutNotReady
		}
	}
	return nil
}

func (m *MemStore) RollbackProjectReleaseWithBindings(ctx context.Context, accountID, projectID, environment, expectedActiveID, sourceID string, ttl int, members []ProjectReleaseMember, fences []BindingPromotionFence) (ProjectReleaseSet, error) {
	ctx, err := checkedReleaseRollbackContext(ctx, sourceID, expectedActiveID)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	return m.ActivateProjectReleaseWithBindings(ctx, accountID, projectID, environment, expectedActiveID, nil, ttl, members, fences)
}

func (m *MemStore) DeactivateProjectReleaseWithBindings(ctx context.Context, accountID, projectID, environment, expectedActiveID string, fallback []ProjectReleaseMember, fences []BindingPromotionFence) (ProjectReleaseSet, error) {
	ctx, err := checkedReleaseDeactivationContext(ctx, accountID, environment, expectedActiveID, fallback, fences)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	return m.deactivateProjectReleaseSet(ctx, accountID, projectID, environment, expectedActiveID, fallback)
}
func (m *MemStore) checkCheckedDeactivationLocked(ctx context.Context, accountID, projectID, environment string, fallback []ProjectReleaseMember) error {
	byApp := map[string]string{}
	for _, member := range fallback {
		byApp[member.AppID] = member.DeploymentID
	}
	count := 0
	for _, app := range m.apps {
		if app.AccountID != accountID || app.ProjectID != projectID || app.Status == AppDeleted || app.PreviewOfSlug != "" {
			continue
		}
		count++
		if m.environmentGitOpsManagedForScopeLocked(app.ID, environment) {
			return ErrConflict
		}
		dep, ok := m.deployments[byApp[app.ID]]
		if !ok || dep.AppID != app.ID || dep.Scope != environment || dep.Status != DeployLive || dep.TrafficPercent != 100 || dep.ImageDigest == "" || dep.RootfsKey == "" || dep.EnvironmentWorkloadHeld() {
			return ErrConflict
		}
	}
	if count != len(fallback) || count == 0 {
		return ErrConflict
	}
	if err := m.checkCheckedReleaseGraphLocked(ctx, byApp, environment); err != nil {
		return err
	}
	for _, fence := range bindingReleaseFences(ctx) {
		if !fence.ValidUntil.IsZero() && !time.Now().Before(fence.ValidUntil) {
			return ErrBindingPromotionExpired
		}
	}
	return nil
}

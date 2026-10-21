package state

import (
	"context"
	"maps"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func releaseKey(projectID, scope string) string {
	return projectID + "\x00" + normalizedDeploymentScope(scope)
}

func (m *MemStore) validRetainedRevisionLocked(deploymentID string) bool {
	return m.deploymentRevisionRetainedLocked(deploymentID)
}

func (m *MemStore) PublishProjectReleaseSet(ctx context.Context, accountID, projectID, environment string, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	return m.publishProjectReleaseSet(ctx, accountID, projectID, environment, nil, nil, ttlSeconds, members)
}

func (m *MemStore) PublishProjectReleaseSetIfActive(ctx context.Context, accountID, projectID, environment, expectedActiveID string, expectedFallback []ProjectReleaseMember, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	return m.publishProjectReleaseSet(ctx, accountID, projectID, environment, &expectedActiveID, expectedFallback, ttlSeconds, members)
}

func (m *MemStore) publishProjectReleaseSet(ctx context.Context, accountID, projectID, environment string, expectedActiveID *string, expectedFallback []ProjectReleaseMember, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	if !validReleaseTTL(ttlSeconds) || len(members) == 0 || len(members) > api.ProjectReleaseSetMaxMembers || !api.ValidProjectEnvironmentSlug(environment) {
		return ProjectReleaseSet{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ProjectReleaseSet{}, err
	}
	return m.publishGuardedProjectReleaseSetLocked(ctx, accountID, projectID, environment, expectedActiveID, expectedFallback, ttlSeconds, members)
}
func (m *MemStore) publishGuardedProjectReleaseSetLocked(ctx context.Context, accountID, projectID, environment string, expectedActiveID *string, expectedFallback []ProjectReleaseMember, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	return m.publishProjectReleaseWithIDLocked(ctx, accountID, projectID, environment, expectedActiveID, expectedFallback, ttlSeconds, members, "")
}
func (m *MemStore) publishProjectReleaseWithIDLocked(ctx context.Context, accountID, projectID, environment string, expectedActiveID *string, expectedFallback []ProjectReleaseMember, ttlSeconds int, members []ProjectReleaseMember, activateID string, changes ...*releaseAdditionChange) (ProjectReleaseSet, error) {
	if !validReleaseTTL(ttlSeconds) || len(members) == 0 || len(members) > api.ProjectReleaseSetMaxMembers {
		return ProjectReleaseSet{}, ErrInvalidArgument
	}
	if environment == "production" && !m.migrationPublicationAllowedLocked(projectID) {
		return ProjectReleaseSet{}, ErrConflict
	}
	for _, member := range members {
		if m.migrationDeploymentRetiredLocked(member.DeploymentID) {
			return ProjectReleaseSet{}, ErrConflict
		}
	}
	defer m.invalidateChangedLifecycleSuccessorsLocked()
	project, ok := m.projects[projectID]
	if !ok || project.AccountID != accountID {
		return ProjectReleaseSet{}, ErrNotFound
	}
	envFound := false
	for _, env := range m.projectEnvironments {
		if env.ProjectID == projectID && env.Slug == environment {
			envFound = true
			break
		}
	}
	if !envFound {
		return ProjectReleaseSet{}, ErrNotFound
	}
	byApp := make(map[string]string, len(members))
	for _, member := range members {
		if _, err := uuid.Parse(member.AppID); err != nil {
			return ProjectReleaseSet{}, ErrInvalidArgument
		}
		if _, err := uuid.Parse(member.DeploymentID); err != nil {
			return ProjectReleaseSet{}, ErrInvalidArgument
		}
		if _, exists := byApp[member.AppID]; exists {
			return ProjectReleaseSet{}, ErrInvalidArgument
		}
		byApp[member.AppID] = member.DeploymentID
	}
	var change *releaseAdditionChange
	if len(changes) > 0 {
		change = changes[0]
	}
	records := map[string]projectReleaseAdditionRecord{}
	for id, record := range m.projectReleaseAdditions {
		if app := m.apps[id]; app.ProjectID == projectID && app.Status != AppDeleted {
			records[id] = record
		}
	}
	if change != nil {
		baseline, ok := m.projectReleaseSets[change.Safe.PreviousReleaseID]
		if !ok {
			return ProjectReleaseSet{}, ErrConflict
		}
		if err := guardReleaseAdditionMembership(change, baseline.Members, byApp); err != nil {
			return ProjectReleaseSet{}, err
		}
		for _, addition := range change.Safe.Additions {
			if app := m.apps[addition.AppID]; app.ProjectID != projectID || ProjectReleaseAdditionFingerprint(app) != addition.Fingerprint {
				return ProjectReleaseSet{}, ErrConflict
			}
		}
	}
	omitted, err := allowedReleaseAdditionOmissions(records, byApp, change)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	count := 0
	for _, app := range m.apps {
		if app.ProjectID != projectID || app.AccountID != accountID || app.Status == AppDeleted || app.PreviewOfSlug != "" {
			continue
		}
		if omitted[app.ID] {
			continue
		}
		count++
		depID, exists := byApp[app.ID]
		if !exists || app.Manifest.RevisionPinTTLSeconds < ttlSeconds {
			return ProjectReleaseSet{}, ErrConflict
		}
		dep, ok := m.deployments[depID]
		if !ok || dep.EnvironmentWorkloadHeld() || dep.AppID != app.ID || normalizedDeploymentScope(dep.Scope) != normalizedDeploymentScope(environment) || dep.Status != DeployLive ||
			(dep.TrafficPercent <= 0 && !dep.TrafficPercentExplicit && !m.validRetainedRevisionLocked(depID) && !m.deploymentInUsableReleaseLocked(depID)) {
			return ProjectReleaseSet{}, ErrConflict
		}
	}
	if count != len(byApp) {
		return ProjectReleaseSet{}, ErrConflict
	}
	if _, checked := ctx.Value(checkedReleaseGraphKey{}).(string); checked {
		if err := m.checkCheckedReleaseGraphLocked(ctx, byApp, environment); err != nil {
			return ProjectReleaseSet{}, err
		}
	} else if err := m.rejectUncheckedBindingReleaseGraphLocked(projectID, environment); err != nil {
		return ProjectReleaseSet{}, err
	}
	key := releaseKey(projectID, environment)
	previousID := m.activeProjectReleaseSets[key]
	if expectedActiveID != nil && previousID != *expectedActiveID {
		return ProjectReleaseSet{}, ErrConflict
	}
	if expectedActiveID != nil && *expectedActiveID == "" {
		if err := m.validateProjectReleaseFallbackLocked(projectID, environment, expectedFallback); err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	if err := m.validateGitOpsReleaseMembersLocked(previousID, byApp); err != nil {
		return ProjectReleaseSet{}, err
	}
	if activateID != "" {
		existing, ok := m.projectReleaseSets[activateID]
		if !ok || existing.Active || !releasePubliclyUsable(existing, time.Now()) || existing.AccountID != accountID || existing.ProjectID != projectID || existing.EnvironmentSlug != environment || !sameReleaseMembers(existing.Members, members) {
			return ProjectReleaseSet{}, ErrConflict
		}
	}
	if _, checked := ctx.Value(checkedReleaseGraphKey{}).(string); checked {
		for _, fence := range bindingReleaseFences(ctx) {
			if !fence.ValidUntil.IsZero() && !time.Now().Before(fence.ValidUntil) {
				return ProjectReleaseSet{}, ErrBindingPromotionExpired
			}
		}
	}
	rollback, recovering := ctx.Value(checkedReleaseRollbackKey{}).(checkedReleaseRollback)
	if recovering {
		if _, checked := ctx.Value(checkedReleaseGraphKey{}).(string); !checked {
			return ProjectReleaseSet{}, ErrBindingReleaseRequired
		}
		if err := validateCheckedRollbackSource(m.projectReleaseSets[rollback.source], accountID, projectID, environment, byApp, time.Now()); err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	if previousID != "" {
		previous := m.projectReleaseSets[previousID]
		previous.Active = false
		expires := time.Now().UTC().Add(time.Duration(previous.TTLSeconds) * time.Second)
		previous.ExpiresAt = &expires
		for _, member := range previous.Members {
			dep := m.deployments[member.DeploymentID]
			if dep.Status == DeployLive && dep.TrafficPercent == 0 {
				if pin, ok := m.revisionPins[member.DeploymentID]; !ok || pin.Before(expires) {
					m.revisionPins[member.DeploymentID] = expires
				}
			}
		}
		m.projectReleaseSets[previousID] = previous
	}
	now := time.Now().UTC()
	release := ProjectReleaseSet{ID: uuid.NewString(), AccountID: accountID, ProjectID: projectID, EnvironmentSlug: environment, Active: true,
		TTLSeconds: ttlSeconds, CreatedAt: now, Members: append([]ProjectReleaseMember(nil), members...)}
	if activateID != "" {
		release.ID = activateID
		release.CreatedAt = m.projectReleaseSets[activateID].CreatedAt
	}
	if recovering {
		release.RollbackSourceReleaseID, release.RollbackPreviousReleaseID = rollback.source, rollback.previous
	}
	m.projectReleaseSets[release.ID] = release
	m.activeProjectReleaseSets[key] = release.ID
	if change != nil {
		for _, addition := range change.Safe.Additions {
			record := m.projectReleaseAdditions[addition.AppID]
			record.Dormant = change.Rollback
			m.projectReleaseAdditions[addition.AppID] = record
		}
	}
	return release, nil
}

func (m *MemStore) DeactivateProjectReleaseSetIfActive(ctx context.Context, accountID, projectID, environment, expectedActiveID string, expectedFallback []ProjectReleaseMember) error {
	_, err := m.deactivateProjectReleaseSet(ctx, accountID, projectID, environment, expectedActiveID, expectedFallback)
	return err
}

func (m *MemStore) deactivateProjectReleaseSet(ctx context.Context, accountID, projectID, environment, expectedActiveID string, expectedFallback []ProjectReleaseMember) (ProjectReleaseSet, error) {
	if expectedActiveID == "" {
		return ProjectReleaseSet{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ownsReleaseEnvironmentLocked(accountID, projectID, environment) {
		return ProjectReleaseSet{}, ErrNotFound
	}
	key := releaseKey(projectID, environment)
	activeID := m.activeProjectReleaseSets[key]
	if activeID != expectedActiveID {
		return ProjectReleaseSet{}, ErrConflict
	}
	for id, record := range m.projectReleaseAdditions {
		if app := m.apps[id]; app.ProjectID == projectID && app.Status != AppDeleted && record.Dormant {
			return ProjectReleaseSet{}, ErrConflict
		}
	}
	release, ok := m.projectReleaseSets[activeID]
	if !ok || !release.Active {
		return ProjectReleaseSet{}, ErrConflict
	}
	if m.releaseSetHasManagedMembersLocked(release) {
		return ProjectReleaseSet{}, ErrConflict
	}
	if err := m.validateProjectReleaseFallbackLocked(projectID, environment, expectedFallback); err != nil {
		return ProjectReleaseSet{}, err
	}
	checked, _ := ctx.Value(checkedReleaseDeactivationKey{}).(bool)
	if checked {
		if err := m.checkCheckedDeactivationLocked(ctx, accountID, projectID, environment, expectedFallback); err != nil {
			return ProjectReleaseSet{}, err
		}
	} else if err := m.rejectUncheckedBindingReleaseGraphLocked(projectID, environment); err != nil {
		return ProjectReleaseSet{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(time.Duration(release.TTLSeconds) * time.Second)
	release.Active = false
	release.ExpiresAt = &expires
	if checked {
		release.Deactivation = projectDeactivationReceipt(ctx, release.ID, expectedFallback, now)
	}
	for _, member := range release.Members {
		dep := m.deployments[member.DeploymentID]
		if dep.Status == DeployLive && dep.TrafficPercent == 0 {
			if pin, exists := m.revisionPins[member.DeploymentID]; !exists || pin.Before(expires) {
				m.revisionPins[member.DeploymentID] = expires
			}
		}
	}
	m.projectReleaseSets[activeID] = release
	delete(m.activeProjectReleaseSets, key)
	return cloneProjectReleaseSet(release), nil
}

func (m *MemStore) validateProjectReleaseFallbackLocked(projectID, environment string, expected []ProjectReleaseMember) error {
	appIDs := make([]string, 0)
	for _, app := range m.apps {
		if app.ProjectID == projectID && app.Status != AppDeleted && app.PreviewOfSlug == "" {
			appIDs = append(appIDs, app.ID)
		}
	}
	return m.validateProjectReleaseFallbackForAppsLocked(appIDs, environment, expected)
}

func (m *MemStore) validateProjectReleaseFallbackForAppsLocked(appIDs []string, environment string, expected []ProjectReleaseMember) error {
	expectedByApp := make(map[string]string, len(expected))
	for _, member := range expected {
		if _, err := uuid.Parse(member.AppID); err != nil {
			return ErrInvalidArgument
		}
		if _, err := uuid.Parse(member.DeploymentID); err != nil {
			return ErrInvalidArgument
		}
		if _, exists := expectedByApp[member.AppID]; exists {
			return ErrInvalidArgument
		}
		expectedByApp[member.AppID] = member.DeploymentID
	}
	apps := make(map[string]struct{}, len(appIDs))
	for _, appID := range appIDs {
		apps[appID] = struct{}{}
	}
	for appID := range expectedByApp {
		if _, ok := apps[appID]; !ok {
			return ErrConflict
		}
	}
	for appID := range apps {
		want := expectedByApp[appID]
		var live []Deployment
		for _, dep := range m.deployments {
			if dep.AppID == appID && normalizedDeploymentScope(dep.Scope) == normalizedDeploymentScope(environment) &&
				dep.Status == DeployLive && dep.TrafficPercent > 0 {
				live = append(live, dep)
			}
		}
		if want == "" {
			if len(live) != 0 {
				return ErrConflict
			}
			continue
		}
		if len(live) != 1 || live[0].ID != want || live[0].TrafficPercent != 100 {
			return ErrConflict
		}
	}
	return nil
}

var _ ProjectReleaseSetPromotionStore = (*MemStore)(nil)

func (m *MemStore) publishProjectReleaseSetLocked(accountID, projectID, environment string, ttlSeconds int, members []ProjectReleaseMember, allowGitOpsReplacement bool, bindingGrants ...*environmentBindingPublication) (ProjectReleaseSet, error) {
	if !validReleaseTTL(ttlSeconds) || len(members) == 0 && !allowGitOpsReplacement || len(members) > api.ProjectReleaseSetMaxMembers || !api.ValidProjectEnvironmentSlug(environment) {
		return ProjectReleaseSet{}, ErrInvalidArgument
	}
	project, ok := m.projects[projectID]
	if !ok || project.AccountID != accountID {
		return ProjectReleaseSet{}, ErrNotFound
	}
	envFound := false
	for _, env := range m.projectEnvironments {
		if env.ProjectID == projectID && env.Slug == environment {
			envFound = true
			break
		}
	}
	if !envFound {
		return ProjectReleaseSet{}, ErrNotFound
	}
	byApp := make(map[string]string, len(members))
	for _, member := range members {
		if _, err := uuid.Parse(member.AppID); err != nil {
			return ProjectReleaseSet{}, ErrInvalidArgument
		}
		if _, err := uuid.Parse(member.DeploymentID); err != nil {
			return ProjectReleaseSet{}, ErrInvalidArgument
		}
		if _, exists := byApp[member.AppID]; exists {
			return ProjectReleaseSet{}, ErrInvalidArgument
		}
		byApp[member.AppID] = member.DeploymentID
	}
	excluded := map[string]bool{}
	if allowGitOpsReplacement {
		excluded = m.workloadReleaseExclusionsLocked(EnvironmentGitSource{AccountID: accountID, ProjectID: projectID, EnvironmentSlug: environment})
	}
	count := 0
	for _, app := range m.apps {
		if excluded[app.ID] || app.ProjectID != projectID || app.AccountID != accountID || app.Status == AppDeleted || app.PreviewOfSlug != "" {
			continue
		}
		count++
		depID, exists := byApp[app.ID]
		if !exists || app.Manifest.RevisionPinTTLSeconds < ttlSeconds {
			return ProjectReleaseSet{}, ErrConflict
		}
		dep, ok := m.deployments[depID]
		if !ok || dep.EnvironmentWorkloadHeld() || dep.AppID != app.ID || normalizedDeploymentScope(dep.Scope) != normalizedDeploymentScope(environment) || dep.Status != DeployLive ||
			(dep.TrafficPercent <= 0 && !dep.TrafficPercentExplicit && !m.validRetainedRevisionLocked(depID) && !m.deploymentInUsableReleaseLocked(depID)) {
			return ProjectReleaseSet{}, ErrConflict
		}
	}
	if count != len(byApp) {
		return ProjectReleaseSet{}, ErrConflict
	}
	if err := m.rejectUncheckedBindingReleaseGraphLocked(projectID, environment); err != nil {
		if !allowGitOpsReplacement || len(bindingGrants) != 1 || bindingGrants[0] == nil || !maps.Equal(bindingGrants[0].members, byApp) {
			return ProjectReleaseSet{}, err
		}
		grant := bindingGrants[0]
		if grant.retirement != nil {
			source := grant.retirement.lease.Source
			if len(members) != 0 || source.AccountID != accountID || source.ProjectID != projectID || source.EnvironmentSlug != environment || m.checkEnvironmentBindingRetirementLocked(*grant.retirement, true) != nil {
				return ProjectReleaseSet{}, err
			}
		} else if !candidateBindingMembersMatch(grant.graph, members) {
			return ProjectReleaseSet{}, err
		}
		if !time.Now().Before(bindingGrants[0].validUntil) {
			return ProjectReleaseSet{}, ErrBindingPromotionExpired
		}
	}
	key := releaseKey(projectID, environment)
	previousID := m.activeProjectReleaseSets[key]
	if !allowGitOpsReplacement {
		if err := m.validateGitOpsReleaseMembersLocked(previousID, byApp); err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	if previousID != "" {
		previous := m.projectReleaseSets[previousID]
		previous.Active = false
		expires := time.Now().UTC().Add(time.Duration(previous.TTLSeconds) * time.Second)
		previous.ExpiresAt = &expires
		for _, member := range previous.Members {
			dep := m.deployments[member.DeploymentID]
			if dep.Status == DeployLive && dep.TrafficPercent == 0 {
				if pin, ok := m.revisionPins[member.DeploymentID]; !ok || pin.Before(expires) {
					m.revisionPins[member.DeploymentID] = expires
				}
			}
		}
		m.projectReleaseSets[previousID] = previous
	}
	now := time.Now().UTC()
	release := ProjectReleaseSet{ID: uuid.NewString(), AccountID: accountID, ProjectID: projectID, EnvironmentSlug: environment, Active: true,
		TTLSeconds: ttlSeconds, CreatedAt: now, Members: append([]ProjectReleaseMember(nil), members...)}
	m.projectReleaseSets[release.ID] = release
	m.activeProjectReleaseSets[key] = release.ID
	return release, nil
}

func (m *MemStore) validateGitOpsReleaseMembersLocked(activeID string, proposed map[string]string) error {
	if activeID == "" {
		return nil
	}
	active, ok := m.projectReleaseSets[activeID]
	if !ok || !active.Active {
		return ErrConflict
	}
	for _, member := range active.Members {
		deployment, exists := m.deployments[member.DeploymentID]
		if exists && deployment.EnvironmentWorkloadManaged() && proposed[member.AppID] != member.DeploymentID {
			return ErrConflict
		}
	}
	return nil
}

func (m *MemStore) releaseSetHasManagedMembersLocked(release ProjectReleaseSet) bool {
	for _, member := range release.Members {
		if deployment, ok := m.deployments[member.DeploymentID]; ok && deployment.EnvironmentWorkloadManaged() {
			return true
		}
	}
	return false
}

func (m *MemStore) PublishProjectEnvironmentPromotionReleaseSet(_ context.Context, accountID, promotionID string, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	promotion, ok := m.projectEnvironmentPromotions[promotionID]
	if !ok || promotion.AccountID != accountID {
		return ProjectReleaseSet{}, ErrNotFound
	}
	if err := m.guardPromotionHealthPublishLocked(promotion); err != nil {
		return ProjectReleaseSet{}, err
	}
	key := releaseKey(promotion.ProjectID, promotion.ToEnvironment)
	activeID := m.activeProjectReleaseSets[key]
	if promotion.TargetReleaseSetID != "" {
		if activeID != promotion.TargetReleaseSetID {
			return ProjectReleaseSet{}, ErrConflict
		}
		release, ok := m.projectReleaseSets[promotion.TargetReleaseSetID]
		if !ok || !release.Active {
			return ProjectReleaseSet{}, ErrConflict
		}
		if err := m.validateProjectEnvironmentPromotionConfigLocked(promotion, false); err != nil {
			return ProjectReleaseSet{}, err
		}
		if err := m.guardPromotionReleaseConfigurationLocked(promotion, release.Members, false, true); err != nil {
			return ProjectReleaseSet{}, err
		}
		if _, err := m.promotionWorkloadActivationsLocked(promotion, release.Members, false, true); err != nil {
			return ProjectReleaseSet{}, err
		}
		if _, err := m.preparePromotionFeatureFlagsLocked(promotion, false, true); err != nil {
			return ProjectReleaseSet{}, err
		}
		return cloneProjectReleaseSet(release), nil
	}
	if activeID != promotion.PreviousTargetReleaseSetID {
		return ProjectReleaseSet{}, ErrConflict
	}
	if err := m.verifyProjectEnvironmentPromotionQualificationLocked(promotion); err != nil {
		return ProjectReleaseSet{}, err
	}
	if promotion.PreviousTargetReleaseSetID == "" {
		var expected []ProjectReleaseMember
		for _, workload := range m.projectEnvironmentPromotionWorkloads[promotionID] {
			if workload.PreviousTargetDeploymentID == "" {
				continue
			}
			for _, app := range m.apps {
				if app.ProjectID == promotion.ProjectID && app.Slug == workload.WorkloadSlug {
					expected = append(expected, ProjectReleaseMember{AppID: app.ID, DeploymentID: workload.PreviousTargetDeploymentID})
				}
			}
		}
		if err := m.validateProjectReleaseFallbackLocked(promotion.ProjectID, promotion.ToEnvironment, expected); err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	if err := m.validateProjectEnvironmentPromotionConfigLocked(promotion, false); err != nil {
		return ProjectReleaseSet{}, err
	}
	if err := m.guardPromotionReleaseConfigurationLocked(promotion, members, false, false); err != nil {
		return ProjectReleaseSet{}, err
	}
	changes, err := m.promotionWorkloadActivationsLocked(promotion, members, false, false)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	flagVersion, err := m.preparePromotionFeatureFlagsLocked(promotion, false, false)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	release, err := m.publishProjectReleaseSetLocked(accountID, promotion.ProjectID, promotion.ToEnvironment, ttlSeconds, members, false)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	m.applyPromotionWorkloadActivationsLocked(changes)
	m.applyPromotionFeatureFlagsLocked(promotion, flagVersion, false)
	promotion.TargetReleaseSetID = release.ID
	if h := m.promotionHealth[promotionID]; h != nil {
		next := clonePromotionHealth(h)
		if next.Policy.Worker != nil {
			next.WorkerHealth.PendingJobs, next.WorkerHealth.OldestPendingAgeMS = m.workerHealthBacklogLocked(next.Policy.Worker, m.clock())
		}
		m.promotionHealth[promotionID] = promotionHealthAfterCutover(next, m.clock())
	}
	if promotion.SyncConfig {
		promotion.TargetConfigVersion = m.appendProjectEnvironmentPromotionConfigLocked(promotion, false)
	}
	promotion.UpdatedAt = time.Now().UTC()
	m.projectEnvironmentPromotions[promotionID] = promotion
	return cloneProjectReleaseSet(release), nil
}

func (m *MemStore) RollbackProjectEnvironmentPromotionReleaseSet(_ context.Context, accountID, promotionID string, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	promotion, ok := m.projectEnvironmentPromotions[promotionID]
	if !ok || promotion.AccountID != accountID {
		return ProjectReleaseSet{}, ErrNotFound
	}
	if promotion.PreviousTargetReleaseSetID == "" || promotion.TargetReleaseSetID == "" {
		return ProjectReleaseSet{}, ErrConflict
	}
	key := releaseKey(promotion.ProjectID, promotion.ToEnvironment)
	activeID := m.activeProjectReleaseSets[key]
	if promotion.RollbackReleaseSetID != "" {
		if activeID != promotion.RollbackReleaseSetID {
			return ProjectReleaseSet{}, ErrConflict
		}
		release, ok := m.projectReleaseSets[promotion.RollbackReleaseSetID]
		if !ok || !release.Active {
			return ProjectReleaseSet{}, ErrConflict
		}
		if err := m.validateProjectEnvironmentPromotionConfigLocked(promotion, true); err != nil {
			return ProjectReleaseSet{}, err
		}
		if err := m.guardPromotionReleaseConfigurationLocked(promotion, release.Members, true, true); err != nil {
			return ProjectReleaseSet{}, err
		}
		if _, err := m.promotionWorkloadActivationsLocked(promotion, release.Members, true, true); err != nil {
			return ProjectReleaseSet{}, err
		}
		if _, err := m.preparePromotionFeatureFlagsLocked(promotion, true, true); err != nil {
			return ProjectReleaseSet{}, err
		}
		return cloneProjectReleaseSet(release), nil
	}
	if h := m.promotionHealth[promotionID]; h != nil && h.Control != nil {
		baseline, ok := m.projectReleaseSets[promotion.PreviousTargetReleaseSetID]
		if !ok || !baseline.Active && (baseline.ExpiresAt == nil || !m.clock().Before(*baseline.ExpiresAt)) {
			return ProjectReleaseSet{}, ErrConflict
		}
	}
	if err := guardPromotionControlRollback(m.promotionHealth[promotionID], m.clock().UTC()); err != nil {
		return ProjectReleaseSet{}, err
	}
	if activeID != promotion.TargetReleaseSetID {
		return ProjectReleaseSet{}, ErrConflict
	}
	if err := m.validateProjectEnvironmentPromotionConfigLocked(promotion, true); err != nil {
		return ProjectReleaseSet{}, err
	}
	if err := m.guardPromotionReleaseConfigurationLocked(promotion, members, true, false); err != nil {
		return ProjectReleaseSet{}, err
	}
	changes, err := m.promotionWorkloadActivationsLocked(promotion, members, true, false)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	flagVersion, err := m.preparePromotionFeatureFlagsLocked(promotion, true, false)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	release, err := m.publishProjectReleaseSetLocked(accountID, promotion.ProjectID, promotion.ToEnvironment, ttlSeconds, members, false)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	m.applyPromotionWorkloadActivationsLocked(changes)
	m.applyPromotionFeatureFlagsLocked(promotion, flagVersion, true)
	promotion.RollbackReleaseSetID = release.ID
	promotion.RestoredTargetReleaseSetID = release.ID
	if promotion.SyncConfig {
		promotion.RollbackConfigVersion = m.appendProjectEnvironmentPromotionConfigLocked(promotion, true)
	}
	promotion.UpdatedAt = time.Now().UTC()
	m.projectEnvironmentPromotions[promotionID] = promotion
	return cloneProjectReleaseSet(release), nil
}

func (m *MemStore) releaseTargetLiveLocked(appID, deploymentID string) bool {
	dep, ok := m.deployments[deploymentID]
	if !ok || dep.AppID != appID || dep.Status != DeployLive || m.migrationDeploymentRetiredLocked(deploymentID) {
		return false
	}
	if dep.TrafficPercent > 0 {
		return true
	}
	expires, ok := m.revisionPins[deploymentID]
	return ok && time.Now().Before(expires) || m.operationRetainsDeploymentLocked(deploymentID) || m.deploymentInUsableReleaseLocked(deploymentID)
}

func (m *MemStore) deploymentInUsableReleaseLocked(deploymentID string) bool {
	for _, release := range m.projectReleaseSets {
		if !m.releaseUsableLocked(release) {
			continue
		}
		for _, member := range release.Members {
			if member.DeploymentID == deploymentID {
				return true
			}
		}
	}
	return false
}

func releaseMemberForApp(release ProjectReleaseSet, appID string) string {
	for _, member := range release.Members {
		if member.AppID == appID {
			return member.DeploymentID
		}
	}
	return ""
}

func (m *MemStore) releaseUsableLocked(release ProjectReleaseSet) bool {
	return release.Active || (release.ExpiresAt != nil && time.Now().Before(*release.ExpiresAt)) || m.operationRetainsReleaseLocked(release)
}

func releasePubliclyUsable(release ProjectReleaseSet, now time.Time) bool {
	return release.Active || (release.ExpiresAt != nil && now.Before(*release.ExpiresAt))
}

func (m *MemStore) ResolveProjectRelease(_ context.Context, appID, scope, requestedID string) (string, string, error) {
	if requestedID != "" {
		if _, err := uuid.Parse(requestedID); err != nil {
			return "", "", ErrInvalidArgument
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resolveProjectReleaseLocked(appID, scope, requestedID)
}

func (m *MemStore) resolveProjectReleaseLocked(appID, scope, requestedID string) (string, string, error) {
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted {
		return "", "", ErrNotFound
	}
	if app.ProjectID == "" {
		if requestedID != "" {
			return "", "", ErrNotFound
		}
		return "", "", nil
	}
	id := requestedID
	if id == "" {
		id = m.activeProjectReleaseSets[releaseKey(app.ProjectID, scope)]
	}
	if id == "" {
		return "", "", nil
	}
	release, ok := m.projectReleaseSets[id]
	if !ok || release.AccountID != app.AccountID || release.ProjectID != app.ProjectID || release.EnvironmentSlug != normalizedDeploymentScope(scope) || !releasePubliclyUsable(release, time.Now()) {
		if requestedID != "" {
			return "", "", ErrNotFound
		}
		return "", "", nil
	}
	depID := releaseMemberForApp(release, appID)
	if depID == "" || !m.releaseTargetLiveLocked(appID, depID) {
		return "", "", ErrConflict
	}
	return id, depID, nil
}

func (m *MemStore) ResolveServiceRelease(_ context.Context, callerAppID, callerDeploymentID, targetAppID, requestedID string) (string, string, error) {
	if callerDeploymentID == "" {
		m.mu.Lock()
		defer m.mu.Unlock()
		callerApp, callerOK := m.apps[callerAppID]
		targetApp, targetOK := m.apps[targetAppID]
		if callerOK && targetOK && callerApp.ProjectID != "" && callerApp.ProjectID == targetApp.ProjectID {
			for _, release := range m.projectReleaseSets {
				if release.ProjectID == callerApp.ProjectID && release.Active {
					return "", "", ErrConflict
				}
			}
		}
		if requestedID != "" {
			return "", "", ErrConflict
		}
		return "", "", nil
	}
	if requestedID != "" {
		if _, err := uuid.Parse(requestedID); err != nil {
			return "", "", ErrInvalidArgument
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var matching *ProjectReleaseSet
	for _, release := range m.projectReleaseSets {
		if requestedID != "" && release.ID != requestedID {
			continue
		}
		if !m.releaseUsableLocked(release) || releaseMemberForApp(release, callerAppID) != callerDeploymentID || releaseMemberForApp(release, targetAppID) == "" {
			continue
		}
		if matching != nil {
			return "", "", ErrConflict
		}
		copy := release
		matching = &copy
	}
	if matching == nil {
		if requestedID != "" {
			return "", "", ErrNotFound
		}
		callerApp, callerOK := m.apps[callerAppID]
		targetApp, targetOK := m.apps[targetAppID]
		if callerOK && targetOK && callerApp.ProjectID != "" && callerApp.ProjectID == targetApp.ProjectID {
			for _, release := range m.projectReleaseSets {
				if release.ProjectID == callerApp.ProjectID && release.Active {
					return "", "", ErrConflict
				}
			}
		}
		return "", "", nil
	}
	depID := releaseMemberForApp(*matching, targetAppID)
	if !m.releaseTargetLiveLocked(targetAppID, depID) {
		return "", "", ErrConflict
	}
	return matching.ID, depID, nil
}

var _ ProjectReleaseSetStore = (*MemStore)(nil)
var _ ProjectReleaseSetStore = (*PgStore)(nil)

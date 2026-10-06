package state

import (
	"context"
	"encoding/json"
	"strings"
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
	members = append([]ProjectReleaseMember(nil), members...)
	for i, member := range members {
		if id, err := uuid.Parse(member.DeploymentID); err == nil {
			if dep, ok := m.deployments[id.String()]; ok {
				members[i].DeploymentID = dep.ID
			} else if dep, ok := m.deployments[strings.ReplaceAll(id.String(), "-", "")]; ok {
				members[i].DeploymentID = dep.ID
			}
		}
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
	count := 0
	for _, app := range m.apps {
		if app.ProjectID != projectID || app.AccountID != accountID || app.Status == AppDeleted || app.PreviewOfSlug != "" {
			continue
		}
		count++
		depID, exists := byApp[app.ID]
		if !exists || app.Manifest.RevisionPinTTLSeconds < ttlSeconds {
			return ProjectReleaseSet{}, ErrConflict
		}
		dep, ok := m.deployments[depID]
		if !ok || dep.AppID != app.ID || normalizedDeploymentScope(dep.Scope) != normalizedDeploymentScope(environment) || dep.Status != DeployLive ||
			(dep.TrafficPercent <= 0 && !dep.TrafficPercentExplicit && !m.validRetainedRevisionLocked(depID) && !m.deploymentInUsableReleaseLocked(depID)) {
			return ProjectReleaseSet{}, ErrConflict
		}
	}
	if count != len(byApp) {
		return ProjectReleaseSet{}, ErrConflict
	}
	if projectReleaseEligibilityOnly(ctx) {
		return ProjectReleaseSet{}, nil
	}
	key := releaseKey(projectID, environment)
	previousID := m.activeProjectReleaseSets[key]
	if expectedActiveID != nil && previousID != *expectedActiveID {
		return ProjectReleaseSet{}, ErrConflict
	}
	if expectedActiveID != nil && *expectedActiveID == "" && !checkedProjectRelease(ctx) {
		if err := m.validateProjectReleaseFallbackLocked(projectID, environment, expectedFallback); err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	now := time.Now().UTC()
	release := ProjectReleaseSet{ID: uuid.NewString(), AccountID: accountID, ProjectID: projectID, EnvironmentSlug: environment, Active: true,
		TTLSeconds: ttlSeconds, CreatedAt: now, Members: append([]ProjectReleaseMember(nil), members...)}
	var auditData []byte
	if checkedProjectRelease(ctx) {
		var err error
		auditData, err = projectReleaseBindingAudit(ctx, release, previousID)
		if err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	if err := m.checkProjectReleaseBindingsLocked(ctx, projectID, environment, members); err != nil {
		return ProjectReleaseSet{}, err
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
	m.projectReleaseSets[release.ID] = release
	m.activeProjectReleaseSets[key] = release.ID
	if checkedProjectRelease(ctx) {
		accountUUID := uuid.MustParse(accountID)
		m.appendAuditLogLocked(AuditLog{ID: uuid.New(), Kind: "project.release_set_checked", AccountID: &accountUUID, AccountEmail: m.accounts[accountID].Email, Actor: "apid", ReceivedAt: time.Now().UTC(), Data: auditData})
	}

	return release, nil
}

func (m *MemStore) DeactivateProjectReleaseSetIfActive(_ context.Context, accountID, projectID, environment, expectedActiveID string, expectedFallback []ProjectReleaseMember) error {
	if expectedActiveID == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ownsReleaseEnvironmentLocked(accountID, projectID, environment) {
		return ErrNotFound
	}
	key := releaseKey(projectID, environment)
	activeID := m.activeProjectReleaseSets[key]
	if activeID != expectedActiveID {
		return ErrConflict
	}
	release, ok := m.projectReleaseSets[activeID]
	if !ok || !release.Active {
		return ErrConflict
	}
	if err := m.validateProjectReleaseFallbackLocked(projectID, environment, expectedFallback); err != nil {
		return err
	}
	if err := m.rejectUncheckedBindingReleaseGraphLocked(projectID, environment); err != nil {
		return err
	}
	now := time.Now().UTC()
	expires := now.Add(time.Duration(release.TTLSeconds) * time.Second)
	release.Active = false
	release.ExpiresAt = &expires
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
	return nil
}

func (m *MemStore) validateProjectReleaseFallbackLocked(projectID, environment string, expected []ProjectReleaseMember) error {
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
	apps := make(map[string]struct{})
	for _, app := range m.apps {
		if app.ProjectID == projectID && app.Status != AppDeleted && app.PreviewOfSlug == "" {
			apps[app.ID] = struct{}{}
		}
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

func (m *MemStore) publishProjectReleaseSetLocked(ctx context.Context, accountID, projectID, environment string, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	if !validReleaseTTL(ttlSeconds) || len(members) == 0 || len(members) > api.ProjectReleaseSetMaxMembers || !api.ValidProjectEnvironmentSlug(environment) {
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
	count := 0
	for _, app := range m.apps {
		if app.ProjectID != projectID || app.AccountID != accountID || app.Status == AppDeleted || app.PreviewOfSlug != "" {
			continue
		}
		count++
		depID, exists := byApp[app.ID]
		if !exists || app.Manifest.RevisionPinTTLSeconds < ttlSeconds {
			return ProjectReleaseSet{}, ErrConflict
		}
		dep, ok := m.deployments[depID]
		if !ok || dep.AppID != app.ID || normalizedDeploymentScope(dep.Scope) != normalizedDeploymentScope(environment) || dep.Status != DeployLive ||
			(dep.TrafficPercent <= 0 && !dep.TrafficPercentExplicit && !m.validRetainedRevisionLocked(depID) && !m.deploymentInUsableReleaseLocked(depID)) {
			return ProjectReleaseSet{}, ErrConflict
		}
	}
	if count != len(byApp) {
		return ProjectReleaseSet{}, ErrConflict
	}
	if err := m.checkProjectReleaseBindingsLocked(ctx, projectID, environment, members); err != nil {
		return ProjectReleaseSet{}, err
	}
	key := releaseKey(projectID, environment)
	if previousID := m.activeProjectReleaseSets[key]; previousID != "" {
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

func (m *MemStore) PublishProjectEnvironmentPromotionReleaseSet(ctx context.Context, accountID, promotionID string, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	promotion, ok := m.projectEnvironmentPromotions[promotionID]
	if !ok || promotion.AccountID != accountID {
		return ProjectReleaseSet{}, ErrNotFound
	}
	report, checked := bindingProjectPromotionReport(ctx)
	var reportJSON []byte
	if checked {
		if _, err := json.Marshal(bindingReleaseFences(ctx)); err != nil {
			return ProjectReleaseSet{}, err
		}
		var err error
		reportJSON, err = json.Marshal(report)
		if err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	if promotion.BindingsRequired {
		if !checked || !checkedProjectRelease(ctx) {
			return ProjectReleaseSet{}, ErrBindingReleaseRequired
		}
		if err := m.validBindingProjectPromotionClaimLocked(bindingProjectPromotionClaim(ctx)); err != nil {
			return ProjectReleaseSet{}, err
		}
		if err := validateBindingProjectPromotionReport(promotion, ttlSeconds, members, report, bindingReleaseFences(ctx)); err != nil {
			return ProjectReleaseSet{}, err
		}
	} else if checked {
		return ProjectReleaseSet{}, ErrConflict
	}
	if checked {
		rows := m.projectEnvironmentPromotionWorkloads[promotionID]
		if len(rows) != len(members) {
			return ProjectReleaseSet{}, ErrConflict
		}
		for _, w := range rows {
			appID := ""
			for _, app := range m.apps {
				if app.AccountID == accountID && app.ProjectID == promotion.ProjectID && app.Slug == w.WorkloadSlug {
					appID = app.ID
				}
			}
			found := false
			for _, member := range members {
				found = found || member.AppID == appID && member.DeploymentID == w.TargetDeploymentID
			}
			if !found || (w.Status != "promoted" && w.Status != "unchanged") {
				return ProjectReleaseSet{}, ErrConflict
			}
		}
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
	changes, err := m.promotionWorkloadActivationsLocked(promotion, members, false, false)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	flagVersion, err := m.preparePromotionFeatureFlagsLocked(promotion, false, false)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	if checked {
		if err := m.validBindingProjectPromotionClaimLocked(bindingProjectPromotionClaim(ctx)); err != nil {
			return ProjectReleaseSet{}, err
		}
	}
	release, err := m.publishProjectReleaseSetLocked(ctx, accountID, promotion.ProjectID, promotion.ToEnvironment, ttlSeconds, members)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	m.applyPromotionWorkloadActivationsLocked(changes)
	m.applyPromotionFeatureFlagsLocked(promotion, flagVersion, false)
	promotion.TargetReleaseSetID = release.ID
	if promotion.SyncConfig {
		promotion.TargetConfigVersion = m.appendProjectEnvironmentPromotionConfigLocked(promotion, false)
	}
	if checked {
		now := time.Now().UTC()
		promotion.Status = "succeeded"
		promotion.Error = ""
		promotion.CompletedAt = &now
		promotion.VerificationStatus = "verified"
		promotion.VerificationError = ""
		promotion.VerificationStartedAt = &now
		promotion.VerificationCompletedAt = &now
		promotion.BindingWorkerToken = ""
		promotion.BindingWorkerUntil = nil
		promotion.BindingCheckNextAt = nil
		promotion.BindingsCheck = reportJSON
		rows := m.projectEnvironmentPromotionWorkloads[promotionID]
		for i := range rows {
			rows[i].VerificationStatus = "verified"
			rows[i].VerificationError = ""
			rows[i].UpdatedAt = now
		}
		m.projectEnvironmentPromotionWorkloads[promotionID] = rows

		data, _ := json.Marshal(map[string]any{"promotion_id": promotion.ID, "project_id": promotion.ProjectID, "from_environment": promotion.FromEnvironment, "to_environment": promotion.ToEnvironment, "release_id": release.ID, "bindings_check": report, "binding_fences": bindingReleaseFences(ctx)})
		accountUUID := uuid.MustParse(accountID)
		m.appendAuditLogLocked(AuditLog{ID: uuid.New(), Kind: "project.environment.promoted", AccountID: &accountUUID, AccountEmail: m.accounts[accountID].Email, Actor: "apid", ReceivedAt: now, Data: data})
	}
	promotion.UpdatedAt = time.Now().UTC()
	m.projectEnvironmentPromotions[promotionID] = promotion
	return cloneProjectReleaseSet(release), nil
}

func (m *MemStore) RollbackProjectEnvironmentPromotionReleaseSet(ctx context.Context, accountID, promotionID string, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	promotion, ok := m.projectEnvironmentPromotions[promotionID]
	if !ok || promotion.AccountID != accountID {
		return ProjectReleaseSet{}, ErrNotFound
	}
	if promotion.BindingsRequired {
		return ProjectReleaseSet{}, ErrBindingReleaseRequired
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
		if _, err := m.promotionWorkloadActivationsLocked(promotion, release.Members, true, true); err != nil {
			return ProjectReleaseSet{}, err
		}
		if _, err := m.preparePromotionFeatureFlagsLocked(promotion, true, true); err != nil {
			return ProjectReleaseSet{}, err
		}
		return cloneProjectReleaseSet(release), nil
	}
	if activeID != promotion.TargetReleaseSetID {
		return ProjectReleaseSet{}, ErrConflict
	}
	if err := m.validateProjectEnvironmentPromotionConfigLocked(promotion, true); err != nil {
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
	release, err := m.publishProjectReleaseSetLocked(ctx, accountID, promotion.ProjectID, promotion.ToEnvironment, ttlSeconds, members)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	m.applyPromotionWorkloadActivationsLocked(changes)
	m.applyPromotionFeatureFlagsLocked(promotion, flagVersion, true)
	promotion.RollbackReleaseSetID = release.ID
	if promotion.SyncConfig {
		promotion.RollbackConfigVersion = m.appendProjectEnvironmentPromotionConfigLocked(promotion, true)
	}
	promotion.UpdatedAt = time.Now().UTC()
	m.projectEnvironmentPromotions[promotionID] = promotion
	return cloneProjectReleaseSet(release), nil
}

func (m *MemStore) releaseTargetLiveLocked(appID, deploymentID string) bool {
	dep, ok := m.deployments[deploymentID]
	if !ok || dep.AppID != appID || dep.Status != DeployLive {
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

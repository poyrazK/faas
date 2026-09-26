package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func releaseKey(projectID, scope string) string {
	return projectID + "\x00" + normalizedDeploymentScope(scope)
}

func (m *MemStore) validRetainedRevisionLocked(deploymentID string) bool {
	expires, ok := m.revisionPins[deploymentID]
	return ok && time.Now().Before(expires)
}

func (m *MemStore) PublishProjectReleaseSet(_ context.Context, accountID, projectID, environment string, ttlSeconds int, members []ProjectReleaseMember) (ProjectReleaseSet, error) {
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

func (m *MemStore) releaseTargetLiveLocked(appID, deploymentID string) bool {
	dep, ok := m.deployments[deploymentID]
	if !ok || dep.AppID != appID || dep.Status != DeployLive {
		return false
	}
	if dep.TrafficPercent > 0 {
		return true
	}
	expires, ok := m.revisionPins[deploymentID]
	return ok && time.Now().Before(expires) || m.deploymentInUsableReleaseLocked(deploymentID)
}

func (m *MemStore) deploymentInUsableReleaseLocked(deploymentID string) bool {
	for _, release := range m.projectReleaseSets {
		if !releaseUsable(release) {
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

func releaseUsable(release ProjectReleaseSet) bool {
	return release.Active || (release.ExpiresAt != nil && time.Now().Before(*release.ExpiresAt))
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
	if !ok || release.ProjectID != app.ProjectID || release.EnvironmentSlug != normalizedDeploymentScope(scope) || !releaseUsable(release) {
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
		if !releaseUsable(release) || releaseMemberForApp(release, callerAppID) != callerDeploymentID || releaseMemberForApp(release, targetAppID) == "" {
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

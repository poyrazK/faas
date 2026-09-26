package state

import (
	"context"
	"sort"
	"strings"
	"time"
)

var _ ProjectEnvironmentPreviewLifecycleStore = (*MemStore)(nil)

func (m *MemStore) UpdateProjectEnvironmentPreviewHead(
	_ context.Context, accountID, projectID string, prNumber int, headSHA string, expiresAt time.Time,
) (ProjectEnvironment, error) {
	if !validProjectEnvironmentPreviewIdentity(prNumber, headSHA, false) || !expiresAt.After(time.Now()) {
		return ProjectEnvironment{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	environment, ok := m.projectEnvironmentByPreviewPRLocked(accountID, projectID, prNumber)
	if !ok {
		return ProjectEnvironment{}, ErrNotFound
	}
	if environment.PreviewState == ProjectEnvironmentPreviewTearingDown {
		return ProjectEnvironment{}, ErrConflict
	}
	environment.PreviewHeadSHA = strings.ToLower(headSHA)
	environment.PreviewState = ProjectEnvironmentPreviewOpen
	expiresAt = expiresAt.UTC()
	environment.PreviewExpiresAt = &expiresAt
	environment.UpdatedAt = time.Now().UTC()
	m.projectEnvironments[environment.ID] = environment
	return environment, nil
}

func (m *MemStore) CloseProjectEnvironmentPreview(
	_ context.Context, accountID, projectID string, prNumber int, closedUntil time.Time,
) (ProjectEnvironment, error) {
	if prNumber <= 0 || !closedUntil.After(time.Now()) {
		return ProjectEnvironment{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	environment, ok := m.projectEnvironmentByPreviewPRLocked(accountID, projectID, prNumber)
	if !ok {
		return ProjectEnvironment{}, ErrNotFound
	}
	if environment.PreviewState == ProjectEnvironmentPreviewTearingDown {
		return ProjectEnvironment{}, ErrConflict
	}
	if environment.PreviewState == ProjectEnvironmentPreviewOpen {
		closedUntil = closedUntil.UTC()
		environment.PreviewExpiresAt = &closedUntil
	}
	environment.PreviewState = ProjectEnvironmentPreviewClosed
	environment.UpdatedAt = time.Now().UTC()
	m.projectEnvironments[environment.ID] = environment
	return environment, nil
}

func (m *MemStore) ListProjectEnvironmentPreviewsForTeardown(
	_ context.Context, now time.Time, limit int,
) ([]ProjectEnvironment, error) {
	if now.IsZero() || limit <= 0 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ProjectEnvironment, 0)
	for _, environment := range m.projectEnvironments {
		if environment.PreviewPRNumber > 0 && environment.PreviewExpiresAt != nil && !environment.PreviewExpiresAt.After(now) {
			out = append(out, environment)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PreviewExpiresAt.Equal(*out[j].PreviewExpiresAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].PreviewExpiresAt.Before(*out[j].PreviewExpiresAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) BeginProjectEnvironmentPreviewTeardown(
	_ context.Context, accountID, projectID, slug string, now, drainUntil time.Time,
) (ProjectEnvironment, []ProjectEnvironmentPreviewDeployment, error) {
	if slug == "" || now.IsZero() || !drainUntil.After(now) {
		return ProjectEnvironment{}, nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	environment, err := m.projectEnvironmentBySlugLocked(projectID, slug)
	if err != nil || environment.AccountID != accountID {
		return ProjectEnvironment{}, nil, ErrNotFound
	}
	if environment.PreviewPRNumber <= 0 || environment.PreviewExpiresAt == nil {
		return ProjectEnvironment{}, nil, ErrConflict
	}
	if environment.PreviewExpiresAt.After(now) {
		return ProjectEnvironment{}, nil, ErrConflict
	}
	alreadyTearingDown := environment.PreviewState == ProjectEnvironmentPreviewTearingDown
	if !alreadyTearingDown {
		drainUntil = drainUntil.UTC()
		environment.PreviewState = ProjectEnvironmentPreviewTearingDown
		environment.PreviewExpiresAt = &drainUntil
		environment.UpdatedAt = time.Now().UTC()
		m.projectEnvironments[environment.ID] = environment
	}

	projectApps := make(map[string]struct{})
	for _, app := range m.apps {
		if app.AccountID == accountID && app.ProjectID == projectID {
			projectApps[app.ID] = struct{}{}
		}
	}
	deploymentIDs := make([]string, 0)
	for deploymentID, deployment := range m.deployments {
		if _, belongs := projectApps[deployment.AppID]; belongs && deployment.Scope == slug && deployment.Status == DeployLive {
			deploymentIDs = append(deploymentIDs, deploymentID)
		}
	}
	sort.Strings(deploymentIDs)
	deployments := make([]ProjectEnvironmentPreviewDeployment, 0, len(deploymentIDs))
	for _, deploymentID := range deploymentIDs {
		deployment := m.deployments[deploymentID]
		deployment.Status = DeploySuperseded
		deployment.TrafficPercent = 0
		m.deployments[deploymentID] = deployment
		deployments = append(deployments, ProjectEnvironmentPreviewDeployment{DeploymentID: deployment.ID, AppID: deployment.AppID})
	}
	if alreadyTearingDown && len(deployments) > 0 {
		drainUntil = drainUntil.UTC()
		environment.PreviewExpiresAt = &drainUntil
		environment.UpdatedAt = time.Now().UTC()
		m.projectEnvironments[environment.ID] = environment
	}
	return environment, deployments, nil
}

func (m *MemStore) projectEnvironmentByPreviewPRLocked(accountID, projectID string, prNumber int) (ProjectEnvironment, bool) {
	project, ok := m.projects[projectID]
	if !ok || project.AccountID != accountID || prNumber <= 0 {
		return ProjectEnvironment{}, false
	}
	for _, environment := range m.projectEnvironments {
		if environment.ProjectID == projectID && environment.PreviewPRNumber == prNumber {
			return environment, true
		}
	}
	return ProjectEnvironment{}, false
}

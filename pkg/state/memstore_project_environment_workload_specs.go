package state

import (
	"context"
	"time"
)

func workloadSpecHeadKey(environmentID, appID string) string { return environmentID + ":" + appID }

func cloneWorkloadSpec(spec ProjectEnvironmentWorkloadSpec) (ProjectEnvironmentWorkloadSpec, error) {
	settings, err := cloneWorkloadSettings(spec.Settings)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	spec.Settings = settings
	return spec, nil
}

func (m *MemStore) workloadSpecEnvironmentLocked(accountID, projectID, environment, appID string) (ProjectEnvironment, error) {
	project, ok := m.projects[projectID]
	app, appOK := m.apps[appID]
	if !ok || project.AccountID != accountID || !appOK || app.AccountID != accountID || app.ProjectID != projectID || app.Status == AppDeleted {
		return ProjectEnvironment{}, ErrNotFound
	}
	for _, env := range m.projectEnvironments {
		if env.ProjectID == projectID && env.AccountID == accountID && env.Slug == environment {
			return env, nil
		}
	}
	return ProjectEnvironment{}, ErrNotFound
}

func (m *MemStore) PutProjectEnvironmentWorkloadSpec(ctx context.Context, accountID, projectID, environment, appID string, expectedRevision int64, settings ProjectEnvironmentWorkloadSettings) (ProjectEnvironmentWorkloadSpec, error) {
	return m.putProjectEnvironmentWorkloadSpec(ctx, accountID, projectID, environment, appID, expectedRevision, settings, false)
}

func (m *MemStore) PutUnprotectedProjectEnvironmentWorkloadSpec(ctx context.Context, accountID, projectID, environmentID, appID string, expectedRevision int64, settings ProjectEnvironmentWorkloadSettings) (ProjectEnvironmentWorkloadSpec, error) {
	return m.putProjectEnvironmentWorkloadSpec(ctx, accountID, projectID, environmentID, appID, expectedRevision, settings, true)
}

func (m *MemStore) putProjectEnvironmentWorkloadSpec(_ context.Context, accountID, projectID, environment, appID string, expectedRevision int64, settings ProjectEnvironmentWorkloadSettings, guarded bool) (ProjectEnvironmentWorkloadSpec, error) {
	if expectedRevision < 0 {
		return ProjectEnvironmentWorkloadSpec{}, ErrInvalidArgument
	}
	hash, err := WorkloadSettingsHash(settings)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	copy, err := cloneWorkloadSettings(settings)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if guarded {
		env, found := m.projectEnvironments[environment]
		if !found || env.AccountID != accountID || env.ProjectID != projectID {
			return ProjectEnvironmentWorkloadSpec{}, ErrNotFound
		}
		if env.Protected {
			return ProjectEnvironmentWorkloadSpec{}, ErrConflict
		}
		environment = env.Slug
	}
	env, err := m.workloadSpecEnvironmentLocked(accountID, projectID, environment, appID)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	return m.putWorkloadSpecLocked(env, appID, expectedRevision, copy, hash)
}

func (m *MemStore) putWorkloadSpecLocked(env ProjectEnvironment, appID string, expectedRevision int64, copy ProjectEnvironmentWorkloadSettings, hash string) (ProjectEnvironmentWorkloadSpec, error) {
	key := workloadSpecHeadKey(env.ID, appID)
	current := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadHeads[key]]
	if current.Revision != expectedRevision {
		return ProjectEnvironmentWorkloadSpec{}, ErrConflict
	}
	if current.ID != "" && current.Hash == hash {
		return cloneWorkloadSpec(current)
	}
	spec := ProjectEnvironmentWorkloadSpec{
		ID: newID(), AccountID: env.AccountID, ProjectID: env.ProjectID, EnvironmentID: env.ID,
		EnvironmentSlug: env.Slug, AppID: appID, Revision: m.nextWorkloadSpecRevisionLocked(env.ID, appID),
		Hash: hash, Settings: copy, CreatedAt: time.Now().UTC(),
	}
	m.projectEnvironmentWorkloadSpecs[spec.ID] = spec
	m.projectEnvironmentWorkloadHeads[key] = spec.ID
	return cloneWorkloadSpec(spec)
}

func (m *MemStore) ProjectEnvironmentWorkloadSpec(_ context.Context, accountID, projectID, environment, appID string) (ProjectEnvironmentWorkloadSpec, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	env, err := m.workloadSpecEnvironmentLocked(accountID, projectID, environment, appID)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	spec, ok := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadHeads[workloadSpecHeadKey(env.ID, appID)]]
	if !ok {
		return ProjectEnvironmentWorkloadSpec{}, ErrNotFound
	}
	return cloneWorkloadSpec(spec)
}

func (m *MemStore) ProjectEnvironmentWorkloadSpecByID(_ context.Context, accountID, projectID, id string) (ProjectEnvironmentWorkloadSpec, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	spec, ok := m.projectEnvironmentWorkloadSpecs[id]
	if !ok || spec.AccountID != accountID || spec.ProjectID != projectID {
		return ProjectEnvironmentWorkloadSpec{}, ErrNotFound
	}
	if _, err := m.workloadSpecEnvironmentLocked(accountID, projectID, spec.EnvironmentSlug, spec.AppID); err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	return cloneWorkloadSpec(spec)
}

func (m *MemStore) ProjectEnvironmentWorkloadSpecForDeployment(_ context.Context, accountID, projectID, deploymentID string) (ProjectEnvironmentWorkloadSpec, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	deployment, ok := m.deployments[deploymentID]
	spec, found := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadDeploymentSpecs[deploymentID]]
	if !ok || !found || spec.AccountID != accountID || spec.ProjectID != projectID ||
		spec.AppID != deployment.AppID || spec.EnvironmentSlug != workloadEnvironmentSlug(deployment.Scope) {
		return ProjectEnvironmentWorkloadSpec{}, ErrNotFound
	}
	if _, err := m.workloadSpecEnvironmentLocked(accountID, projectID, spec.EnvironmentSlug, spec.AppID); err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	return cloneWorkloadSpec(spec)
}

func (m *MemStore) deleteEnvironmentWorkloadSpecsLocked(environmentID string) {
	for id, spec := range m.projectEnvironmentWorkloadSpecs {
		if spec.EnvironmentID != environmentID {
			continue
		}
		delete(m.projectEnvironmentWorkloadSpecs, id)
		delete(m.projectEnvironmentWorkloadHeads, workloadSpecHeadKey(environmentID, spec.AppID))
		for key, capture := range m.projectEnvironmentPromotionWorkloadSpecs {
			if capture.PreparedSpecID == id {
				delete(m.projectEnvironmentPromotionWorkloadSpecs, key)
			}
		}
		for deploymentID, specID := range m.projectEnvironmentWorkloadDeploymentSpecs {
			if specID == id {
				delete(m.projectEnvironmentWorkloadDeploymentSpecs, deploymentID)
				delete(m.projectEnvironmentQueueRuntimeSets, deploymentID)
			}
		}
	}
}

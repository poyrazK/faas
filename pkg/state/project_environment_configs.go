package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func projectEnvironmentConfigKey(projectID, environmentSlug string) string {
	return projectID + "\x00" + environmentSlug
}

func (m *MemStore) ProjectEnvironmentConfigLatest(_ context.Context, accountID, projectID, environmentSlug string) (ProjectEnvironmentConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	project, ok := m.projects[projectID]
	if !ok || project.AccountID != accountID {
		return ProjectEnvironmentConfig{}, ErrNotFound
	}
	if _, err := m.projectEnvironmentBySlugLocked(projectID, environmentSlug); err != nil {
		return ProjectEnvironmentConfig{}, err
	}
	versions := m.projectEnvironmentConfigs[projectEnvironmentConfigKey(projectID, environmentSlug)]
	if len(versions) == 0 {
		return ProjectEnvironmentConfig{}, ErrNotFound
	}
	return cloneProjectEnvironmentConfig(versions[len(versions)-1]), nil
}

func (m *MemStore) CreateProjectEnvironmentConfigVersion(_ context.Context, config ProjectEnvironmentConfig) (ProjectEnvironmentConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	project, ok := m.projects[config.ProjectID]
	if !ok || project.AccountID != config.AccountID {
		return ProjectEnvironmentConfig{}, ErrNotFound
	}
	if _, err := m.projectEnvironmentBySlugLocked(config.ProjectID, config.EnvironmentSlug); err != nil {
		return ProjectEnvironmentConfig{}, err
	}
	if !json.Valid(config.Values) {
		return ProjectEnvironmentConfig{}, fmt.Errorf("invalid project environment configuration JSON")
	}
	memory, err := m.gitOpsGuardConfigurationLocked(config)
	if err != nil {
		return ProjectEnvironmentConfig{}, err
	}
	key := projectEnvironmentConfigKey(config.ProjectID, config.EnvironmentSlug)
	versions := m.projectEnvironmentConfigs[key]
	config.Version = int64(len(versions) + 1)
	if config.ID == "" {
		config.ID = newID()
	}
	if config.CreatedAt.IsZero() {
		config.CreatedAt = time.Now().UTC()
	}
	config.Values = append(json.RawMessage(nil), config.Values...)
	before := m.latestProjectEnvironmentConfigValuesLocked(config.ProjectID, config.EnvironmentSlug)
	m.projectEnvironmentConfigs[key] = append(versions, config)
	m.projectConfigRuntimeChangeLocked(config.ProjectID, config.EnvironmentSlug, before, config.Values, config.CreatedAt)
	touchGitOpsMemoryIntent(memory)
	return cloneProjectEnvironmentConfig(config), nil
}

func (m *MemStore) projectEnvironmentBySlugLocked(projectID, slug string) (ProjectEnvironment, error) {
	for _, env := range m.projectEnvironments {
		if env.ProjectID == projectID && env.Slug == slug {
			return env, nil
		}
	}
	return ProjectEnvironment{}, ErrNotFound
}

func (m *MemStore) projectEnvironmentConfigLatestLocked(projectID, environment string) ProjectEnvironmentConfig {
	versions := m.projectEnvironmentConfigs[projectEnvironmentConfigKey(projectID, environment)]
	if len(versions) == 0 {
		project := m.projects[projectID]
		return ProjectEnvironmentConfig{
			AccountID: project.AccountID, ProjectID: projectID, EnvironmentSlug: environment,
			ConfigHash: api.EmptyProjectEnvironmentConfigHash(), Values: json.RawMessage(`{}`),
		}
	}
	return cloneProjectEnvironmentConfig(versions[len(versions)-1])
}

func (m *MemStore) validateProjectEnvironmentPromotionConfigLocked(promotion ProjectEnvironmentPromotion, rollback bool) error {
	if !promotion.SyncConfig {
		return nil
	}
	_, sourceHash, sourceErr := api.NormalizeProjectEnvironmentConfig(promotion.SourceConfigSnapshot)
	_, previousHash, previousErr := api.NormalizeProjectEnvironmentConfig(promotion.PreviousTargetConfigSnapshot)
	if sourceErr != nil || previousErr != nil || sourceHash != promotion.SourceConfigHash || previousHash != promotion.PreviousTargetConfigHash {
		return ErrConflict
	}
	target := m.projectEnvironmentConfigLatestLocked(promotion.ProjectID, promotion.ToEnvironment)
	if rollback {
		if promotion.RollbackConfigVersion != 0 {
			if target.Version != promotion.RollbackConfigVersion || target.ConfigHash != promotion.PreviousTargetConfigHash {
				return ErrConflict
			}
			return nil
		}
		if promotion.TargetConfigVersion == 0 || target.Version != promotion.TargetConfigVersion || target.ConfigHash != promotion.SourceConfigHash {
			return ErrConflict
		}
		return nil
	}
	if promotion.TargetConfigVersion != 0 {
		if target.Version != promotion.TargetConfigVersion || target.ConfigHash != promotion.SourceConfigHash {
			return ErrConflict
		}
		return nil
	}
	source := m.projectEnvironmentConfigLatestLocked(promotion.ProjectID, promotion.FromEnvironment)
	if source.ConfigHash != promotion.SourceConfigHash || target.ConfigHash != promotion.PreviousTargetConfigHash {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) appendProjectEnvironmentPromotionConfigLocked(promotion ProjectEnvironmentPromotion, rollback bool) int64 {
	values, hash := promotion.SourceConfigSnapshot, promotion.SourceConfigHash
	if rollback {
		values, hash = promotion.PreviousTargetConfigSnapshot, promotion.PreviousTargetConfigHash
	}
	key := projectEnvironmentConfigKey(promotion.ProjectID, promotion.ToEnvironment)
	versions := m.projectEnvironmentConfigs[key]
	config := ProjectEnvironmentConfig{
		ID: newID(), AccountID: promotion.AccountID, ProjectID: promotion.ProjectID,
		EnvironmentSlug: promotion.ToEnvironment, Version: int64(len(versions) + 1),
		ConfigHash: hash, Values: append(json.RawMessage(nil), values...), CreatedAt: time.Now().UTC(),
	}
	before := m.latestProjectEnvironmentConfigValuesLocked(promotion.ProjectID, promotion.ToEnvironment)
	m.projectEnvironmentConfigs[key] = append(versions, config)
	m.projectConfigRuntimeChangeLocked(promotion.ProjectID, promotion.ToEnvironment, before, config.Values, config.CreatedAt)
	return config.Version
}

// latestProjectEnvironmentConfigValuesLocked returns the latest stored values,
// or nil when the environment has no version yet.
func (m *MemStore) latestProjectEnvironmentConfigValuesLocked(projectID, environment string) json.RawMessage {
	versions := m.projectEnvironmentConfigs[projectEnvironmentConfigKey(projectID, environment)]
	if len(versions) == 0 {
		return nil
	}
	return versions[len(versions)-1].Values
}

// projectConfigRuntimeChangeLocked mirrors the PostgreSQL trigger on
// project_environment_config_versions (ADR-1136): when a new version changes
// the delivered variables, every member app is stamped in the affected scopes
// and its older snapshots there are marked stale.
func (m *MemStore) projectConfigRuntimeChangeLocked(projectID, environment string, before, after json.RawMessage, at time.Time) {
	if !projectConfigRuntimeEnvChanged(before, after) {
		return
	}
	for id, app := range m.apps {
		if app.ProjectID != projectID || app.Status == "deleted" {
			continue
		}
		for _, scope := range projectConfigScopes(environment) {
			if scope == "default" {
				// The default scope reads production's configuration, so its
				// snapshots go stale. Its boundary is not advanced: every
				// environment consults the shared "default" boundary, and a
				// production change must not invalidate its neighbours.
				m.markDefaultScopeSnapshotsStaleLocked(id, at)
				continue
			}
			m.markEnvironmentRuntimeChangedAndSnapshotsLocked(id, scope, at)
		}
	}
}

// markDefaultScopeSnapshotsStaleLocked marks an app's default-scope snapshots
// taken at or before at stale, without advancing any runtime boundary.
func (m *MemStore) markDefaultScopeSnapshotsStaleLocked(appID string, at time.Time) {
	for id, snapshot := range m.snapshots {
		deployment := m.deployments[snapshot.DeploymentID]
		if deployment.AppID != appID || normalizedDeploymentScope(deployment.Scope) != "default" || snapshot.Stale || snapshot.CreatedAt.After(at) {
			continue
		}
		snapshot.Stale = true
		m.snapshots[id] = snapshot
	}
}

func cloneProjectEnvironmentConfig(config ProjectEnvironmentConfig) ProjectEnvironmentConfig {
	config.Values = append(json.RawMessage(nil), config.Values...)
	return config
}

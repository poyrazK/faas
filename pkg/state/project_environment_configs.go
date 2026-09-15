package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
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
	m.projectEnvironmentConfigs[key] = append(versions, config)
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

func cloneProjectEnvironmentConfig(config ProjectEnvironmentConfig) ProjectEnvironmentConfig {
	config.Values = append(json.RawMessage(nil), config.Values...)
	return config
}

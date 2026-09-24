package state

import "context"

// RollbackProjectEnvironmentClone removes an environment created by a failed
// clone saga. It refuses to remove live deployments or resource-managed
// secrets so compensation cannot orphan an external binding.
func (m *MemStore) RollbackProjectEnvironmentClone(_ context.Context, accountID, projectID, slug string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	project, ok := m.projects[projectID]
	if !ok || project.AccountID != accountID {
		return ErrNotFound
	}
	var environmentID string
	for id, candidate := range m.projectEnvironments {
		if candidate.AccountID == accountID && candidate.ProjectID == projectID && candidate.Slug == slug {
			environmentID = id
			break
		}
	}
	if environmentID == "" {
		return ErrNotFound
	}
	if slug == "production" {
		return ErrConflict
	}
	for _, app := range m.apps {
		if app.ProjectID != projectID {
			continue
		}
		for _, deployment := range m.deployments {
			if deployment.AppID == app.ID && normalizedDeploymentScope(deployment.Scope) == slug {
				return ErrConflict
			}
		}
		for key, secret := range m.secrets {
			if key.AppID == app.ID && secret.Scope == slug &&
				(secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "") {
				return ErrConflict
			}
		}
	}

	for key, env := range m.envs {
		if env.Scope == slug {
			if app, ok := m.apps[key.AppID]; ok && app.ProjectID == projectID {
				delete(m.envs, key)
			}
		}
	}
	for key, secret := range m.secrets {
		if secret.Scope == slug {
			if app, ok := m.apps[key.AppID]; ok && app.ProjectID == projectID {
				delete(m.secrets, key)
			}
		}
	}
	delete(m.projectEnvironments, environmentID)
	delete(m.projectEnvironmentConfigs, projectEnvironmentConfigKey(projectID, slug))
	for id, approval := range m.projectEnvironmentApprovals {
		if approval.AccountID == accountID && approval.ProjectSlug == project.Slug && approval.EnvironmentSlug == slug {
			delete(m.projectEnvironmentApprovals, id)
		}
	}
	return nil
}

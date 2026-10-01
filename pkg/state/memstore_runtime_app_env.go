package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ RuntimeAppEnvStore = (*MemStore)(nil)

func (m *MemStore) RuntimeAppEnvForDeployment(_ context.Context, accountID, appID, deploymentID string) (RuntimeAppEnvSnapshot, error) {
	if err := validateRuntimeAppEnvIDs(accountID, appID, deploymentID); err != nil {
		return RuntimeAppEnvSnapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, appOK := m.apps[appID]
	d, deploymentOK := m.deployments[deploymentID]
	if !appOK || !deploymentOK || app.AccountID != accountID || app.Status == AppDeleted || d.AppID != appID || !d.DeploymentAliasActive() {
		return RuntimeAppEnvSnapshot{}, ErrNotFound
	}
	if app.ProjectID != "" {
		project, ok := m.projects[app.ProjectID]
		if !ok || project.AccountID != accountID {
			return RuntimeAppEnvSnapshot{}, ErrNotFound
		}
	}
	scope := normalizedDeploymentScope(d.Scope)
	if api.ValidateScope(scope) != nil {
		return RuntimeAppEnvSnapshot{}, ErrConflict
	}
	ownerID := m.deploymentRuntimeEnvironmentOwners[d.ID]
	pinID := m.projectEnvironmentWorkloadDeploymentSpecs[d.ID]
	if ownerID != "" {
		env, envOK := m.projectEnvironments[ownerID]
		spec, specOK := m.projectEnvironmentWorkloadSpecs[pinID]
		if !envOK || !specOK || env.AccountID != accountID || env.ProjectID != app.ProjectID || env.Slug != workloadEnvironmentSlug(scope) ||
			spec.EnvironmentID != env.ID || spec.EnvironmentSlug != env.Slug || spec.AccountID != accountID || spec.ProjectID != app.ProjectID || spec.AppID != appID {
			return RuntimeAppEnvSnapshot{}, ErrNotFound
		}
	} else {
		// A lost owner must not downgrade a pinned deployment to the legacy path.
		if pinID != "" {
			return RuntimeAppEnvSnapshot{}, ErrNotFound
		}
		if invocationStageScope(scope) && app.ProjectID != "" {
			env, err := m.workloadSpecEnvironmentLocked(accountID, app.ProjectID, scope, appID)
			if err != nil || env.CreatedAt.After(d.CreatedAt) {
				return RuntimeAppEnvSnapshot{}, ErrNotFound
			}
			ownerID = env.ID
		}
	}
	result := RuntimeAppEnvSnapshot{AccountID: accountID, AppID: appID, DeploymentID: deploymentID, Scope: scope, EnvironmentID: ownerID, Values: []AppEnv{}}
	for _, value := range m.envs {
		if value.AccountID == accountID && value.AppID == appID && value.Scope == scope {
			result.Values = append(result.Values, value)
		}
	}
	sort.Slice(result.Values, func(i, j int) bool { return result.Values[i].Key < result.Values[j].Key })
	return result, nil
}

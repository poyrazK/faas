package state

import (
	"context"
	"errors"
)

// ProjectEnvironmentCloneValueScope follows the deployment actually serving
// production. Runtime values are strictly scoped: named production and legacy
// default values must not be merged by key. Undeployed workloads preserve
// explicitly configured production values, otherwise they use legacy default.
func ProjectEnvironmentCloneValueScope(ctx context.Context, store Store, accountID, appID, source string) (string, error) {
	if source != "production" {
		return source, nil
	}
	deployment, err := ResolveProductionDeployment(ctx, store, appID)
	if err == nil {
		return normalizedDeploymentScope(deployment.Scope), nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	values, err := store.ListAppEnvInScope(ctx, accountID, appID, source)
	if err != nil {
		return "", err
	}
	secrets, err := store.ListAppSecretsInScope(ctx, accountID, appID, source)
	if err != nil {
		return "", err
	}
	if len(values) > 0 || len(secrets) > 0 {
		return source, nil
	}
	return DefaultEnvScope, nil
}

func validateCloneValueScopes(actual, expected map[string]string) error {
	if expected == nil {
		return nil
	}
	if len(actual) != len(expected) {
		return ErrConflict
	}
	for appID, scope := range actual {
		if expected[appID] != scope {
			return ErrConflict
		}
	}
	return nil
}

func (m *MemStore) projectCloneValueScopesLocked(apps map[string]string, clone ProjectEnvironmentClone) (map[string]string, error) {
	scopes := make(map[string]string, len(apps))
	for appID := range apps {
		scope := clone.SourceSlug
		if scope == "production" {
			if id := m.activeProjectReleaseSets[releaseKey(clone.ProjectID, scope)]; id != "" {
				release := m.projectReleaseSets[id]
				deployment, ok := m.deployments[releaseMemberForApp(release, appID)]
				if !ok || deployment.AppID != appID || deployment.Scope != scope || deployment.Status != DeployLive {
					return nil, ErrConflict
				}
			} else {
				var deployments []Deployment
				for _, deployment := range m.deployments {
					if deployment.AppID == appID && deployment.Status == DeployLive {
						deployments = append(deployments, deployment)
					}
				}
				var selected Deployment
				for _, deployment := range ProductionRoutingDeployments(deployments) {
					if deployment.TrafficPercent > 0 && (selected.ID == "" || deploymentPreferredForWake(deployment, selected)) {
						selected = deployment
					}
				}
				if selected.ID != "" {
					scope = normalizedDeploymentScope(selected.Scope)
				} else {
					hasValues := false
					for _, value := range m.envs {
						hasValues = hasValues || value.AppID == appID && value.Scope == scope
					}
					for _, secret := range m.secrets {
						hasValues = hasValues || secret.AppID == appID && secret.Scope == scope
					}
					if !hasValues {
						scope = DefaultEnvScope
					}
				}
			}
		}
		scopes[appID] = scope
	}
	return scopes, validateCloneValueScopes(scopes, clone.ExpectedSourceValueScopes)
}

package state

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// projectConfigScopes are the deployment scopes that read environment's
// configuration (ADR-1136). Production is also read by the default scope, the
// mapping workloadEnvironmentSlug applies to a project's implicit environment.
// projectConfigDefaultBoundary is the runtime boundary a production project
// configuration change stamps for default-scope deployments, which read
// production's configuration (ADR-1136). It is separate from the "default"
// boundary because every environment consults that one (default-scope app
// variables are inherited by all scopes), and a production change must not
// mark its neighbours stale. The colon keeps it apart from any environment
// slug.
const projectConfigDefaultBoundary = "default:project-config"

// runtimeBoundaryScopes are the scope-change keys a deployment in scope
// consults.
func runtimeBoundaryScopes(scope string) []string {
	scope = normalizedDeploymentScope(scope)
	if scope == "default" {
		return []string{"default", projectConfigDefaultBoundary}
	}
	return []string{"default", scope}
}

func projectConfigScopes(environment string) []string {
	if environment == "production" {
		return []string{"production", "default"}
	}
	return []string{environment}
}

// inheritedProjectEnv returns the variables a deployment inherits from its
// project environment's configuration, sorted by key, without the keys the
// app sets itself in the same scope.
func inheritedProjectEnv(accountID, appID, scope string, own []AppEnv, config json.RawMessage, at time.Time) ([]AppEnv, error) {
	env, err := api.ProjectEnvironmentConfigEnv(config)
	if err != nil {
		return nil, ErrConflict
	}
	overridden := make(map[string]bool, len(own))
	for _, row := range own {
		overridden[row.Key] = true
	}
	rows := make([]AppEnv, 0, len(env))
	for key, value := range env {
		if overridden[key] {
			continue
		}
		rows = append(rows, AppEnv{AccountID: accountID, AppID: appID, Scope: scope, Key: key, Value: value, CreatedAt: at, UpdatedAt: at})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows, nil
}

// projectConfigRuntimeEnvChanged reports whether two configuration documents
// deliver different variables, so a version that changes only document-only
// keys does not invalidate any snapshot.
func projectConfigRuntimeEnvChanged(before, after json.RawMessage) bool {
	left, leftErr := api.ProjectEnvironmentConfigEnv(before)
	right, rightErr := api.ProjectEnvironmentConfigEnv(after)
	if leftErr != nil || rightErr != nil || len(left) != len(right) {
		return true
	}
	for key, value := range left {
		if other, ok := right[key]; !ok || other != value {
			return true
		}
	}
	return false
}

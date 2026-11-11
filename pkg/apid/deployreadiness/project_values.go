package deployreadiness

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Project values remain available when a paired release replaces app-owned
// values. Read the project layer independently so removing an override exposes
// the inherited key, just as it does when the guest boots.
func loadProjectRuntimeKeys(ctx context.Context, r *report, store Reader, account string, app state.App, baseline state.ProjectReleaseSet, environment string, keys map[string]bool) bool {
	if app.ProjectID == "" || app.PreviewOfSlug != "" {
		return true
	}
	if state.IsProductionScope(environment) {
		environment = "production"
	}
	if err := loadProjectConfigurationKeys(ctx, r, store, account, app, environment, keys); err != nil {
		return projectRuntimeKeysUnavailable(r, app)
	}
	grants, err := projectRuntimeSecretGrants(ctx, r, store, app, baseline, environment)
	if err != nil {
		return projectRuntimeKeysUnavailable(r, app)
	}
	var selection map[string]string
	if len(grants) > 0 && json.Unmarshal(grants, &selection) != nil {
		return projectRuntimeKeysUnavailable(r, app)
	}
	r.identities["secret_grants:"+app.WorkloadName] = grants
	if selection != nil {
		return true // An explicit secret selection never inherits project secrets.
	}
	if err := loadProjectSecretKeys(ctx, r, store, account, app, environment, keys); err != nil {
		return projectRuntimeKeysUnavailable(r, app)
	}
	return true
}

func loadProjectConfigurationKeys(ctx context.Context, r *report, store Reader, account string, app state.App, environment string, keys map[string]bool) error {
	reader, ok := store.(interface {
		ProjectEnvironmentConfigLatest(context.Context, string, string, string) (state.ProjectEnvironmentConfig, error)
	})
	if !ok {
		return state.ErrConflict
	}
	config, err := reader.ProjectEnvironmentConfigLatest(ctx, account, app.ProjectID, environment)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	variables, err := api.ProjectEnvironmentConfigEnv(config.Values)
	if err != nil {
		return err
	}
	r.identities["project_config:"+app.WorkloadName] = config.ConfigHash
	for key := range variables {
		keys[key] = true
	}
	return nil
}

func loadProjectSecretKeys(ctx context.Context, r *report, store Reader, account string, app state.App, environment string, keys map[string]bool) error {
	reader, ok := store.(state.ProjectEnvironmentSecretStore)
	if !ok {
		return state.ErrConflict
	}
	secrets, err := reader.ListProjectEnvironmentSecrets(ctx, account, app.ProjectID, environment)
	if err != nil {
		return err
	}
	type logicalSecret struct {
		Version   int64
		ValueHash string
	}
	logical := make(map[string]logicalSecret, len(secrets))
	for _, secret := range secrets {
		keys[secret.Key] = true
		logical[secret.Key] = logicalSecret{secret.SecretVersion, secret.ValueHash}
	}
	r.identities["project_secrets:"+app.WorkloadName] = logical
	return nil
}

func projectRuntimeSecretGrants(ctx context.Context, r *report, store Reader, app state.App, baseline state.ProjectReleaseSet, environment string) (json.RawMessage, error) {
	for _, member := range baseline.Members {
		if member.AppID == app.ID {
			grants, _ := r.identities["secret_grants:"+app.WorkloadName].(json.RawMessage)
			return grants, nil
		}
	}
	reader, ok := store.(interface {
		LiveDeploymentForScope(context.Context, string, string) (state.Deployment, error)
	})
	if !ok || app.ID == "" {
		return nil, nil
	}
	deployment, err := reader.LiveDeploymentForScope(ctx, app.ID, environment)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return deployment.OverrideEnvSecrets, nil
}

func projectRuntimeKeysUnavailable(r *report, app state.App) bool {
	r.add("runtime_inputs_unavailable", "unknown", app.WorkloadName, "Inherited project configuration or secret metadata could not be read.", "Retry when project environment metadata is available.")
	return false
}

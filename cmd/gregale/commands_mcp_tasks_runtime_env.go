package main

import (
	"context"
	"maps"

	"github.com/onebox-faas/faas/pkg/api"
)

// mcpTasksRuntimeApp returns app with Manifest.Env overlaid by the app's
// default-scope runtime variables. GET /v1/apps/{slug} carries only the
// deployed manifest's env; the variables `gregale env set` and `gregale mcp
// tasks configure` write live in app_envs, which the runtime applies over it.
// Reading only the manifest reported every configured namespace and metrics
// slug as missing (MCP Tasks prod probe 2026-11-22). When the variables cannot
// be read, app is returned unchanged and the checks see what they saw before.
func mcpTasksRuntimeApp(ctx context.Context, client *api.Client, app api.AppResponse) api.AppResponse {
	if client == nil || app.Slug == "" {
		return app
	}
	vars, err := client.GetAppsSlugEnvValues(ctx, app.Slug, "")
	if err != nil || len(vars.Env) == 0 {
		return app
	}
	env := make(map[string]string, len(app.Manifest.Env)+len(vars.Env))
	maps.Copy(env, app.Manifest.Env)
	for _, row := range vars.Env {
		if row.Value != nil {
			env[row.Key] = *row.Value
		}
	}
	app.Manifest.Env = env
	return app
}

// mcpTasksPostgresBindingKeys returns the access mode of each environment key
// a ready managed PostgreSQL binding injects in the default scope. A ready
// binding is a platform-managed credential, as good as a secret for the
// readiness checks; a provisioning, failed or retiring one injects nothing
// the app can use yet.
func mcpTasksPostgresBindingKeys(ctx context.Context, client *api.Client, slug string) map[string]string {
	keys := map[string]string{}
	if client == nil || slug == "" {
		return keys
	}
	list, err := client.ListAppPostgresBindings(ctx, slug)
	if err != nil {
		return keys
	}
	for _, binding := range list.Items {
		if binding.State == "ready" && (binding.Scope == "" || binding.Scope == "default") {
			keys[binding.EnvironmentKey] = binding.Access
		}
	}
	return keys
}

// mcpTasksSecretInDefaultScope reports whether secrets holds key in the
// default scope, the one a deployed app reads.
func mcpTasksSecretInDefaultScope(secrets api.AppSecretListResponse, key string) bool {
	for _, secret := range secrets.Secrets {
		if secret.Key == key && (secret.Scope == "" || secret.Scope == "default") {
			return true
		}
	}
	for _, secret := range secrets.SecretsByScope["default"] {
		if secret.Key == key {
			return true
		}
	}
	return false
}

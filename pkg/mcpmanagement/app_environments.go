package mcpmanagement

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *management) appEnvironment(ctx context.Context, slug, project, environment string, write bool) (api.AppResponse, api.ProjectEnvironmentResponse, error) {
	var e api.ProjectEnvironmentResponse
	if err := validateSegment(slug, "app"); err != nil {
		return api.AppResponse{}, e, err
	}
	if environment == "" {
		if project != "" {
			return api.AppResponse{}, e, invalid("invalid_argument", "project requires a named environment")
		}
		if err := m.defaultAccess(write); err != nil {
			return api.AppResponse{}, e, err
		}
		a, err := m.client.GetApp(ctx, slug)
		return a, e, err
	}
	p, e, err := m.environmentAccess(ctx, project, environment, write, false)
	if err != nil {
		return api.AppResponse{}, e, err
	}
	member := false
	for _, w := range p.Workloads {
		member = member || w.Slug == slug
	}
	if !member {
		return api.AppResponse{}, e, invalid("app_access_denied", "app is outside the selected project environment")
	}
	a, err := m.client.GetAppInEnvironment(ctx, slug, environment)
	if err != nil {
		return a, e, err
	}
	if a.ID == "" || a.Slug != slug || !m.appIDAllowed(a.ID) {
		return a, e, invalid("app_access_denied", "app identity is outside consent")
	}
	if m.opts.Environments != nil && m.opts.Environments.Named[project+"/"+environment].Apps[slug] != a.ID {
		return a, e, invalid("app_access_denied", "app identity changed since environment consent; reconnect")
	}
	return a, e, nil
}

func (m *management) scopedHealth(ctx context.Context, args appArgs) (api.AppHealthResponse, error) {
	if args.Environment == "" {
		if args.Project != "" {
			return api.AppHealthResponse{}, invalid("invalid_argument", "project requires a named environment")
		}
		health, err := m.client.GetAppHealth(ctx, args.App)
		if err == nil && normalizedScope(health.Scope) != api.DefaultEnvScope {
			return health, invalid("invalid_response", "API returned another health scope")
		}
		return health, err
	}
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return api.AppHealthResponse{}, err
	}
	var health api.AppHealthResponse
	if args.Environment == "" {
		health, err = m.client.GetAppHealth(ctx, args.App)
	} else {
		health, err = m.client.GetAppHealthInEnvironment(ctx, args.App, args.Environment)
	}
	if err != nil {
		return health, err
	}
	if health.AppID != app.ID || normalizedScope(health.Scope) != normalizedScope(args.Environment) || (env.ID != "" && (health.EnvironmentID != env.ID || health.ProjectID != env.ProjectID)) {
		return health, invalid("invalid_response", "API returned inconsistent environment health identity")
	}
	return health, nil
}

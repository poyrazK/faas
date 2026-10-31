package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

type liveDeploymentStore interface {
	LiveDeployments(context.Context, string) ([]state.Deployment, error)
}

// Ordinary ingress follows the same active production graph or serving lane
// as scheduler wakes. Desired edits and dark candidates cannot change ingress.
// Historical deployments without settings pins retain the App projection.
func (r pgRouter) productionAppSettings(ctx context.Context, app state.App) (state.App, error) {
	if app.ProjectID == "" {
		return app, nil
	}
	deployment, err := state.ResolveProductionDeployment(ctx, publicProductionDeploymentStore{r.store, app}, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		return app, nil
	}
	if err != nil {
		return state.App{}, err
	}
	return state.ResolveAppForDeployment(ctx, r.store, app, deployment)
}

// Adapt the production resolver to the same read-only host snapshot. Its
// bounded deployment projection and active graph must not escape to a pool.
type publicProductionDeploymentStore struct {
	publicHostAppStore
	app state.App
}

func (s publicProductionDeploymentStore) LiveDeployments(ctx context.Context, appID string) ([]state.Deployment, error) {
	if appID != s.app.ID {
		return nil, state.ErrConflict
	}
	return (pgRouter{store: s.publicHostAppStore}).livePublicRoutingDeployments(ctx, s.app)
}

func (s publicProductionDeploymentStore) ResolveProjectRelease(ctx context.Context, appID, scope, requested string) (string, string, error) {
	if appID != s.app.ID || scope != "production" {
		return "", "", state.ErrConflict
	}
	if resolver, ok := s.publicHostAppStore.(interface {
		ResolveProjectRelease(context.Context, string, string, string) (string, string, error)
	}); ok {
		return resolver.ResolveProjectRelease(ctx, appID, scope, requested)
	}
	reader, ok := s.publicHostAppStore.(interface {
		ActiveProjectReleaseSet(context.Context, string, string, string) (state.ProjectReleaseSet, error)
	})
	if !ok {
		return "", "", nil
	}
	release, err := reader.ActiveProjectReleaseSet(ctx, s.app.AccountID, s.app.ProjectID, scope)
	if errors.Is(err, state.ErrNotFound) && requested == "" {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if release.ID == "" || !release.Active || release.AccountID != s.app.AccountID ||
		release.ProjectID != s.app.ProjectID || release.EnvironmentSlug != scope {
		return "", "", state.ErrConflict
	}
	if requested != "" && requested != release.ID {
		return "", "", state.ErrNotFound
	}
	for _, member := range release.Members {
		if member.AppID == appID && member.DeploymentID != "" {
			return release.ID, member.DeploymentID, nil
		}
	}
	return "", "", state.ErrConflict
}

// productionLiveDeployments selects one production routing scope. Legacy
// default traffic remains active while named production candidates are dark;
// once named production serves traffic it replaces the legacy routing set.
// Other environments are never eligible for the ordinary weighted picker.
func productionLiveDeployments(ctx context.Context, store liveDeploymentStore, appID string) ([]state.Deployment, error) {
	deployments, err := store.LiveDeployments(ctx, appID)
	if err != nil {
		return nil, err
	}
	return state.ProductionRoutingDeployments(deployments), nil
}

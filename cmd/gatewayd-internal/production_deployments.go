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
	deployment, err := state.ResolveProductionDeployment(ctx, r.store, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		return app, nil
	}
	if err != nil {
		return state.App{}, err
	}
	return state.ResolveAppForDeployment(ctx, r.store, app, deployment)
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

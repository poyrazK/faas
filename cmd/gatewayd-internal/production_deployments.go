package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

type liveDeploymentStore interface {
	LiveDeployments(context.Context, string) ([]state.Deployment, error)
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

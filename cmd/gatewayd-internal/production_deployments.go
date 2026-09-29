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
	var production, legacy []state.Deployment
	var productionTraffic, legacyTraffic bool
	for _, deployment := range deployments {
		switch deployment.Scope {
		case "production":
			production = append(production, deployment)
			productionTraffic = productionTraffic || deployment.TrafficPercent > 0
		case "", "default":
			legacy = append(legacy, deployment)
			legacyTraffic = legacyTraffic || deployment.TrafficPercent > 0
		}
	}
	if productionTraffic || (!legacyTraffic && len(production) > 0) {
		return production, nil
	}
	return legacy, nil
}

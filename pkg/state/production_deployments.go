package state

import "context"

// ProductionRoutingDeployments selects a single production routing lane.
// Legacy default traffic remains active while named production is dark.
// The result preserves the input order and includes zero-weight siblings for
// callers that retain exact deployment targets; it never includes stages.
func ProductionRoutingDeployments(deployments []Deployment) []Deployment {
	var production, legacy []Deployment
	var productionTraffic, legacyTraffic bool
	for _, deployment := range deployments {
		switch deployment.Scope {
		case "production":
			production = append(production, deployment)
			productionTraffic = productionTraffic || deployment.TrafficPercent > 0
		case "", DefaultEnvScope:
			legacy = append(legacy, deployment)
			legacyTraffic = legacyTraffic || deployment.TrafficPercent > 0
		}
	}
	if productionTraffic || (!legacyTraffic && len(production) > 0) {
		return production
	}
	return legacy
}

// ResolveProductionDeployment resolves an unscoped wake without borrowing a
// stage. An active production graph takes precedence over newer direct
// deployments. Without a graph only traffic-bearing production/default rows
// are eligible; a dark candidate is never implicitly woken as production.
func ResolveProductionDeployment(ctx context.Context, store interface {
	LiveDeployments(context.Context, string) ([]Deployment, error)
	DeploymentByID(context.Context, string) (Deployment, error)
}, appID string) (Deployment, error) {
	if resolver, ok := store.(interface {
		ResolveProjectRelease(context.Context, string, string, string) (string, string, error)
	}); ok {
		_, deploymentID, err := resolver.ResolveProjectRelease(ctx, appID, "production", "")
		if err != nil {
			return Deployment{}, err
		}
		if deploymentID != "" {
			deployment, err := store.DeploymentByID(ctx, deploymentID)
			if err != nil {
				return Deployment{}, err
			}
			if deployment.AppID != appID || deployment.Scope != "production" || deployment.Status != DeployLive {
				return Deployment{}, ErrConflict
			}
			return deployment, nil
		}
	}
	deployments, err := store.LiveDeployments(ctx, appID)
	if err != nil {
		return Deployment{}, err
	}
	var selected Deployment
	for _, deployment := range ProductionRoutingDeployments(deployments) {
		if deployment.Status != DeployLive || deployment.AppID != appID || deployment.TrafficPercent <= 0 {
			continue
		}
		if selected.ID == "" || deploymentPreferredForWake(deployment, selected) {
			selected = deployment
		}
	}
	if selected.ID == "" {
		return Deployment{}, ErrNotFound
	}
	return selected, nil
}

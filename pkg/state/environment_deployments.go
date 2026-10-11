package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// ResolveEnvironmentDeployment selects the active graph member before direct
// traffic-bearing deployments. Desired workload heads and dark candidates do
// not select a warm pool. Callers must still validate the original environment
// owner and resolve the immutable settings of the returned deployment.
func ResolveEnvironmentDeployment(ctx context.Context, store interface {
	LiveDeployments(context.Context, string) ([]Deployment, error)
	DeploymentByID(context.Context, string) (Deployment, error)
}, appID, scope string) (Deployment, error) {
	scope = normalizedDeploymentScope(scope)
	if api.ValidateScope(scope) != nil {
		return Deployment{}, ErrInvalidArgument
	}
	if scope == DefaultEnvScope || scope == "production" {
		return ResolveProductionDeployment(ctx, store, appID)
	}
	if resolver, ok := store.(interface {
		ResolveProjectRelease(context.Context, string, string, string) (string, string, error)
	}); ok {
		_, deploymentID, err := resolver.ResolveProjectRelease(ctx, appID, scope, "")
		if err != nil {
			return Deployment{}, err
		}
		if deploymentID != "" {
			dep, err := store.DeploymentByID(ctx, deploymentID)
			if err != nil {
				return Deployment{}, err
			}
			if dep.AppID != appID || normalizedDeploymentScope(dep.Scope) != scope || dep.Status != DeployLive {
				return Deployment{}, ErrConflict
			}
			return dep, nil
		}
	}
	deployments, err := store.LiveDeployments(ctx, appID)
	if err != nil {
		return Deployment{}, err
	}
	var selected Deployment
	for _, dep := range deployments {
		if dep.AppID == appID && dep.Status == DeployLive && normalizedDeploymentScope(dep.Scope) == scope && dep.TrafficPercent > 0 &&
			(selected.ID == "" || deploymentPreferredForWake(dep, selected)) {
			selected = dep
		}
	}
	if selected.ID == "" {
		return Deployment{}, ErrNotFound
	}
	return selected, nil
}

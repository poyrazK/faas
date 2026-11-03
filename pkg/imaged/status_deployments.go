package imaged

import (
	"context"
	"slices"

	"github.com/onebox-faas/faas/pkg/state"
)

// statusDeploymentLister is the narrowed deployment listing PgStore and
// MemStore provide. The security and evidence sweeps act only on serving
// deployments, so they ask for those instead of the whole history.
type statusDeploymentLister interface {
	ListDeploymentsWithStatus(ctx context.Context, statuses ...state.DeploymentStatus) ([]state.Deployment, error)
}

// deploymentsWithStatus returns the deployments of listed apps in one of
// statuses. A store without the narrowed listing gets the same rows from
// ListAllDeployments.
func (l *Loop) deploymentsWithStatus(ctx context.Context, statuses ...state.DeploymentStatus) ([]state.Deployment, error) {
	if lister, ok := l.store.(statusDeploymentLister); ok {
		return lister.ListDeploymentsWithStatus(ctx, statuses...)
	}
	deployments, err := l.store.ListAllDeployments(ctx)
	if err != nil {
		return nil, err
	}
	out := deployments[:0]
	for _, dep := range deployments {
		if slices.Contains(statuses, dep.Status) {
			out = append(out, dep)
		}
	}
	return out, nil
}

package sched

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// noProductionDeploymentErr is the permanent wake error for an unscoped wake
// that found no traffic-bearing production deployment. A live deployment at
// 0% traffic is never woken as production (ResolveProductionDeployment), but
// "the app has no live deployment" contradicted `gregale deployments`, which
// lists it as live: h12-mcp's first deploy stopped before promotion and every
// request answered that (Hunt 12 H12-59). Name the dark deployment and the
// promotion instead. The extra reads run only on this error path.
func noProductionDeploymentErr(ctx context.Context, store state.Store, appID string) error {
	problem := api.NewProblem(http.StatusNotFound, api.CodeNotFound, "No live deployment", "the app has no live deployment to wake")
	if dark, ok := darkProductionDeployment(ctx, store, appID); ok {
		// The next step goes in Detail: a Problem's Hint does not cross the
		// schedd -> gatewayd gRPC hop (pkg/grpcerr).
		detail := fmt.Sprintf("no deployment receives production traffic; deployment %s is live at 0%% traffic", dark.ID)
		if app, err := store.AppByID(ctx, appID); err == nil && app.Slug != "" {
			detail += fmt.Sprintf(". Promote it with `gregale traffic promote --app %s --deployment %s`", app.Slug, dark.ID)
		}
		problem = api.NewProblem(http.StatusNotFound, api.CodeNotFound, "No live deployment", detail)
	}
	return errors.Join(ErrPermanentWake, problem)
}

// darkProductionDeployment returns the newest live production or default
// deployment that carries no traffic.
func darkProductionDeployment(ctx context.Context, store state.Store, appID string) (state.Deployment, bool) {
	deployments, err := store.LiveDeployments(ctx, appID)
	if err != nil {
		return state.Deployment{}, false
	}
	var dark state.Deployment
	for _, deployment := range state.ProductionRoutingDeployments(deployments) {
		if deployment.Status != state.DeployLive || deployment.AppID != appID || deployment.TrafficPercent > 0 {
			continue
		}
		if dark.ID == "" || deployment.CreatedAt.After(dark.CreatedAt) {
			dark = deployment
		}
	}
	return dark, dark.ID != ""
}

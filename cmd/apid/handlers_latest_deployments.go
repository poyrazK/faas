package main

import (
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// listLatestDeploymentsByApp returns one newest deployment for each non-deleted
// app the authenticated account owns. The store performs the tenant and soft-delete
// filtering; this handler only supplies deterministic response ordering and
// maps the rows through the canonical deployment response builder.
func (s *server) listLatestDeploymentsByApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	latestByApp, err := s.store.ListLatestDeploymentPerApp(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list latest deployments"))
		return
	}

	apps, _ := s.store.ListApps(r.Context(), acct.ID)
	appByID := make(map[string]state.App, len(apps))
	for _, app := range apps {
		appByID[app.ID] = app
	}

	deployments := make([]state.Deployment, 0, len(latestByApp))
	for _, deployment := range latestByApp {
		deployments = append(deployments, deployment)
	}
	sort.Slice(deployments, func(i, j int) bool {
		if deployments[i].CreatedAt.Equal(deployments[j].CreatedAt) {
			return deployments[i].ID > deployments[j].ID
		}
		return deployments[i].CreatedAt.After(deployments[j].CreatedAt)
	})

	response := api.LatestDeploymentsByAppResponse{
		Items: make([]api.DeploymentResponse, 0, len(deployments)),
	}
	for _, deployment := range deployments {
		response.Items = append(response.Items, s.deploymentResponse(deployment, appByID[deployment.AppID]))
	}
	writeJSON(w, http.StatusOK, response)
}

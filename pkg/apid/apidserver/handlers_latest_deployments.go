package apidserver

import (
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
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

	apps, err := s.store.ListApps(r.Context(), acct.ID)
	var visible []state.App
	if err == nil {
		visible, err = appaccess.VisibleApps(r, s.store, actorAccount(r, acct), apps)
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load apps for latest deployments"))
		return
	}
	appByID := make(map[string]state.App, len(apps))
	for _, app := range apps {
		appByID[app.ID] = app
	}
	// The URL names no app, so appaccess.Wrap never decided: a company
	// app's deployment leaves the list once its creator left the company.
	hidden := make(map[string]bool, len(apps)-len(visible))
	for _, app := range apps {
		hidden[app.ID] = true
	}
	for _, app := range visible {
		delete(hidden, app.ID)
	}

	deployments := make([]state.Deployment, 0, len(latestByApp))
	for _, deployment := range latestByApp {
		if !hidden[deployment.AppID] {
			deployments = append(deployments, deployment)
		}
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
		response.Items = append(response.Items, s.deploymentResponse(r.Context(), deployment, appByID[deployment.AppID]))
	}
	writeJSON(w, http.StatusOK, response)
}

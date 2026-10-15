package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apphealth"
	"github.com/onebox-faas/faas/pkg/state"
)

// Read-only evidence projection. Ownership is resolved before any telemetry
// fetch. It never calls a scheduler, VM manager, or customer endpoint.
func (s *server) getAppHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp uses r.Context().
	if !ok {
		return
	}
	app, environment, problem := s.appEnvironmentSettings(w, r, acct, app, false)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	scope := api.DefaultEnvScope
	if environment.ID != "" {
		scope = environment.Slug
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.AppHealthCollectionTimeout)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")
	assessment := apphealth.CollectScope(ctx, s.store, s.promqlClient, app, acct.Plan.PerAppMetricsAllowed(), scope)
	if environment.ID != "" {
		current, err := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, app.ProjectID, scope)
		if err != nil || current.ID != environment.ID {
			api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Environment changed", "Inspect the environment again"))
			return
		}
		assessment.ProjectID, assessment.EnvironmentID = app.ProjectID, environment.ID
	}
	writeJSON(w, http.StatusOK, assessment)
}

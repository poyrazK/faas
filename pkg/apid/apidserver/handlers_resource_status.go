package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/conditions"
	"github.com/onebox-faas/faas/pkg/state"
)

// getAppStatus is GET /v1/apps/{slug}/status (ADR-20261030151810): the app's
// conditions, derived from the same health assessment as /health. Nothing is
// stored and nothing is woken.
func (s *server) getAppStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, assessment, ok := s.collectAppHealth(w, r, acct)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, conditions.App(app.Slug, assessment, time.Now()))
}

// getDeploymentStatus is GET /v1/deployments/{id}/status: the deployment's
// conditions, mapped from the response GET /v1/deployments/{id} returns.
func (s *server) getDeploymentStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, err := s.store.DeploymentByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.notFound(w, "no such deployment")
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such deployment")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, conditions.Deployment(s.deploymentResponse(r.Context(), d, app), time.Now()))
}

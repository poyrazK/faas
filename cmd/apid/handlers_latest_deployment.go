package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getLatestAppDeployment returns the newest deployment for an app. Resolving
// the slug first keeps missing and cross-account apps on the same 404 surface.
func (s *server) getLatestAppDeployment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	deployment, err := s.store.LatestDeployment(r.Context(), app.ID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no deployment for this app")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read latest deployment"))
		return
	}
	writeJSON(w, http.StatusOK, s.deploymentResponse(deployment, app))
}

package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listProfileCanaryChecks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	deploymentID := r.PathValue("deployment")
	id, err := uuid.Parse(deploymentID)
	if err != nil || id.String() != deploymentID {
		api.WriteProblem(w, api.ErrValidation("deployment must be a canonical UUID"))
		return
	}
	limit := api.ProfileCanaryHistoryPageSize
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > api.ProfileCanaryHistoryMaxPage {
			api.WriteProblem(w, api.ErrValidation("invalid canary profile history page limit"))
			return
		}
	}
	before := r.URL.Query().Get("before")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.ProfileCanaryCheckStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("canary profile history is unavailable"))
		return
	}
	page, err := store.ListProfileCanaryChecks(r.Context(), acct.ID, app.ID, deploymentID, limit, before)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid canary profile history cursor or page limit"))
		return
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "app, deployment or canary profile history cursor")
		return
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("canary profile history could not be read"))
		return
	}
	candidate, candidateErr := s.store.DeploymentByID(r.Context(), deploymentID)
	if candidateErr != nil || candidate.AppID != app.ID {
		candidate = state.Deployment{}
	}
	for i := range page.Entries {
		s.enrichCanaryProfileSignal(r.Context(), app, &page.Entries[i], &candidate)
	}
	writeJSON(w, http.StatusOK, page)
}

package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) routeRequirementsTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.RouteRequirementsStore, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.App{}, nil, false
	}
	store, ok := s.store.(state.RouteRequirementsStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("saved route requirements are unavailable"))
	}
	return app, store, ok
}

func (s *server) getSavedRouteRequirements(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.routeRequirementsTarget(w, r, acct)
	if !ok {
		return
	}
	saved, err := store.GetSavedRouteRequirements(r.Context(), acct.ID, app.ID)
	if err != nil {
		s.routeRequirementsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *server) putSavedRouteRequirements(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var request api.SaveRouteRequirementsRequest
	if err := decodeJSONSized(r, &request, api.RoutePolicyRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid saved route requirements request"))
		return
	}
	config, _, err := routerequirements.NormalizeCoverage(request.Requirements)
	if err == nil {
		request.Requirements = config
		config, _, err = state.NormalizeSavedRouteRequirements(request)
	}
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	request.Requirements = config
	app, store, ok := s.routeRequirementsTarget(w, r, acct)
	if !ok {
		return
	}
	saved, err := store.SaveRouteRequirements(r.Context(), acct.ID, app.ID, request)
	if err != nil {
		s.routeRequirementsError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "route_requirements.saved", &acct.ID, map[string]any{"app_id": app.ID, "revision": saved.Revision, "sha256": saved.SHA256})
	writeJSON(w, http.StatusOK, saved)
}

func (s *server) postCheckRouteRequirements(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var request api.CheckRouteRequirementsRequest
	if err := decodeJSONSized(r, &request, api.RoutePolicyRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid route requirements check"))
		return
	}
	if err := state.ValidateCheckRouteRequirements(request); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, store, ok := s.routeRequirementsTarget(w, r, acct)
	if !ok {
		return
	}
	checker := func(snapshot state.RoutePolicySnapshot, saved api.SavedRouteRequirements) (api.RouteRequirementsCheck, error) {
		return routerequirements.BuildSavedCheck(saved, s.routeRequirementsContext(snapshot), string(snapshot.Account.Plan), request.DeploymentID, routePolicyInventory(snapshot.Contract, snapshot.Account.Plan))
	}
	result, err := store.CheckRouteRequirements(r.Context(), acct.ID, app.ID, request, checker)
	if err != nil {
		s.routeRequirementsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) routeRequirementsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "app, saved route requirements or deployment")
	case errors.Is(err, state.ErrRouteRequirementsRevision):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Saved route requirements changed", "Read the current revision before replacing or checking a pinned revision."))
	default:
		api.WriteProblem(w, api.ErrCapacity("saved route requirements could not be read or updated"))
	}
}

package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// routePrioritiesRequestMaxBytes bounds the PUT body (20 rules).
const routePrioritiesRequestMaxBytes = 16 << 10

// getRoutePriorities serves GET /v1/apps/{slug}/route-priorities (ADR-957):
// the rules gateways apply when the app's warm capacity is saturated.
func (s *server) getRoutePriorities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	s.writeRoutePriorities(w, r, acct, app)
}

// putRoutePriorities replaces the app's saved rules. apid is the only writer.
func (s *server) putRoutePriorities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.RoutePriorityStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route priorities"))
		return
	}
	var req api.SetRoutePrioritiesRequest
	if err := decodeJSONSized(r, &req, routePrioritiesRequestMaxBytes); err != nil || req.Routes == nil {
		api.WriteProblem(w, api.ErrValidation("body must be {\"routes\": [...]}; use [] for no priorities"))
		return
	}
	if err := api.ValidateRoutePriorities(req.Routes); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if _, err := store.SetRoutePriorities(r.Context(), acct.ID, app.ID, req.Routes); errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such app")
		return
	} else if err != nil {
		api.WriteProblem(w, api.ErrCapacity("route priorities"))
		return
	}
	s.audit.Emit(r.Context(), "route_priorities.updated", &acct.ID, map[string]any{"app_id": app.ID, "route_count": len(req.Routes)})
	s.writeRoutePriorities(w, r, acct, app)
}

// deleteRoutePriorities removes saved rules, restoring the route-health default.
func (s *server) deleteRoutePriorities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.RoutePriorityStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route priorities"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if err := store.DeleteRoutePriorities(r.Context(), acct.ID, app.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("route priorities"))
		return
	}
	s.audit.Emit(r.Context(), "route_priorities.reset", &acct.ID, map[string]any{"app_id": app.ID})
	s.writeRoutePriorities(w, r, acct, app)
}

func (s *server) writeRoutePriorities(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) {
	source, setting, err := state.EffectiveRoutePriorities(r.Context(), s.store, acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("route priorities"))
		return
	}
	out := api.RoutePrioritiesResponse{Slug: app.Slug, Source: source, Routes: setting.Routes}
	if !setting.UpdatedAt.IsZero() {
		out.UpdatedAt = setting.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, out)
}

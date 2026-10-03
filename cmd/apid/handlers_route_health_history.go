package main

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) routeHealthHistoryTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.RouteHealthHistoryStore, bool) {
	id, err := uuid.Parse(r.PathValue("deployment"))
	if err != nil || id.String() != r.PathValue("deployment") {
		api.WriteProblem(w, api.ErrValidation("deployment must be a canonical UUID"))
		return state.App{}, nil, false
	}
	app, _, ok := s.routeHealthTarget(w, r, acct)
	if !ok {
		return app, nil, false
	}
	store, ok := s.store.(state.RouteHealthHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route health history is unavailable"))
	}
	return app, store, ok
}

func (s *server) getRouteHealthHistoryEntry(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, err := uuid.Parse(r.PathValue("decision_id"))
	if err != nil || id.String() != r.PathValue("decision_id") {
		api.WriteProblem(w, api.ErrValidation("decision_id must be a canonical UUID"))
		return
	}
	app, store, ok := s.routeHealthHistoryTarget(w, r, acct)
	if !ok {
		return
	}
	entry, err := store.GetRouteHealthHistoryEntry(r.Context(), acct.ID, app.ID, r.PathValue("deployment"), id.String())
	if err != nil {
		s.routeHealthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *server) listRouteHealthHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limit := api.RouteHealthHistoryPageSize
	if value := r.URL.Query().Get("limit"); value != "" {
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > api.RouteHealthHistoryMaxPage {
			api.WriteProblem(w, api.ErrValidation("invalid route health history page limit"))
			return
		}
	}
	before := r.URL.Query().Get("before")
	if before != "" {
		if id, err := uuid.Parse(before); err != nil || id.String() != before {
			api.WriteProblem(w, api.ErrValidation("before must be a retained decision UUID"))
			return
		}
	}
	app, store, ok := s.routeHealthHistoryTarget(w, r, acct)
	if !ok {
		return
	}
	page, err := store.ListRouteHealthHistory(r.Context(), acct.ID, app.ID, r.PathValue("deployment"), limit, before)
	if err != nil {
		s.routeHealthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

package main

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) routeHistoryTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.RouteCheckHistoryStore, bool) {
	app, _, ok := s.automaticRouteCheckTarget(w, r, acct)
	if !ok {
		return app, nil, false
	}
	store, ok := s.store.(state.RouteCheckHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route check history is unavailable"))
	}
	return app, store, ok
}

func (s *server) getRouteCheckHistoryEntry(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, err := uuid.Parse(r.PathValue("check_id"))
	if err != nil || id.String() != r.PathValue("check_id") {
		api.WriteProblem(w, api.ErrValidation("check_id must be a canonical UUID"))
		return
	}
	app, store, ok := s.routeHistoryTarget(w, r, acct)
	if !ok {
		return
	}
	entry, err := store.GetRouteCheckHistoryEntry(r.Context(), acct.ID, app.ID, r.PathValue("deployment"), id.String())
	if err != nil {
		s.automaticRouteCheckError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *server) listRouteCheckHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limit := api.RouteCheckHistoryPageSize
	if value := r.URL.Query().Get("limit"); value != "" {
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > api.RouteCheckHistoryMaxPage {
			api.WriteProblem(w, api.ErrValidation("invalid route history page limit"))
			return
		}
	}
	before := r.URL.Query().Get("before")
	if before != "" {
		if id, err := uuid.Parse(before); err != nil || id.String() != before {
			api.WriteProblem(w, api.ErrValidation("before must be a retained check UUID"))
			return
		}
	}
	app, store, ok := s.routeHistoryTarget(w, r, acct)
	if !ok {
		return
	}
	page, err := store.ListRouteCheckHistory(r.Context(), acct.ID, app.ID, r.PathValue("deployment"), limit, before)
	if err != nil {
		s.automaticRouteCheckError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

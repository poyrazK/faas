package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getRouteLifecycleHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limit := api.RouteLifecycleHistoryPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > api.RouteLifecycleHistoryMaxPage {
			api.WriteProblem(w, api.ErrValidation("invalid lifecycle history limit"))
			return
		}
	}
	before := r.URL.Query().Get("before")
	if before != "" {
		id, err := strconv.ParseInt(before, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != before {
			api.WriteProblem(w, api.ErrValidation("before must be a retained positive review ID"))
			return
		}
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.RouteLifecycleHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("lifecycle history is unavailable"))
		return
	}
	page, err := store.ListRouteLifecycleHistory(r.Context(), acct.ID, app.ID, limit, before)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "app or retained lifecycle review cursor")
		case errors.Is(err, state.ErrInvalidArgument):
			api.WriteProblem(w, api.ErrValidation("invalid lifecycle history page"))
		default:
			api.WriteProblem(w, api.ErrCapacity("lifecycle history could not be read"))
		}
		return
	}
	writeJSON(w, http.StatusOK, page)
}

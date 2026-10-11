package main

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listAppHealthHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp uses r.Context().
	if !ok {
		return
	}
	limit, before, err := appHealthHistoryParams(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid health history limit or cursor"))
		return
	}
	store, ok := s.store.(state.AppHealthHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("app health history unavailable"))
		return
	}
	page, err := store.ListAppHealthHistory(r.Context(), acct.ID, app.ID, limit, before, time.Now().UTC())
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "health history cursor")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("app health history unavailable"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, page)
}

func appHealthHistoryParams(r *http.Request) (int, string, error) {
	limit, before := api.AppHealthHistoryPageSize, r.URL.Query().Get("before")
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > api.AppHealthHistoryMaxPage {
			return 0, "", state.ErrInvalidArgument
		}
		limit = parsed
	}
	if before != "" {
		if id, err := uuid.Parse(before); err != nil || id.String() != before {
			return 0, "", state.ErrInvalidArgument
		}
	}
	return limit, before, nil
}

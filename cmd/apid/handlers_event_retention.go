package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func parseEventRetentionQuery(r *http.Request) (api.EventRetentionQuery, error) {
	q := api.EventRetentionQuery{Source: r.URL.Query().Get("source"), App: r.URL.Query().Get("app")}
	for key, values := range r.URL.Query() {
		if len(values) != 1 || key != "source" && key != "app" && key != "window" && key != "limit" {
			return q, state.ErrInvalidArgument
		}
	}
	var err error
	if value := r.URL.Query().Get("window"); value != "" {
		q.Window, err = time.ParseDuration(value)
		if err != nil || q.Window <= 0 {
			return q, state.ErrInvalidArgument
		}
	}
	if value := r.URL.Query().Get("limit"); value != "" {
		q.Limit, err = strconv.Atoi(value)
		if err != nil || q.Limit <= 0 {
			return q, state.ErrInvalidArgument
		}
	}
	return q, q.Validate()
}

func (s *server) getEventRetentionHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	q, err := parseEventRetentionQuery(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid retention source, app, window or sample limit"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.EventRetentionRequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	appID := ""
	if q.App != "" {
		app, ok := s.loadApp(w, r, acct, q.App)
		if !ok {
			return
		}
		appID = app.ID
	}
	store, ok := s.store.(state.EventRetentionStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event retention health"))
		return
	}
	out, err := store.GetEventRetentionHealth(ctx, acct.ID, appID, q, time.Now().UTC())
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid retention query"))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "retention account or application")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Retention health timed out", "retry the request"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("event retention health"))
	default:
		writeJSON(w, http.StatusOK, out)
	}
}

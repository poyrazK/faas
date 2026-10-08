package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getEventConsumerExecutionHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	window, err := api.EventConsumerHealthWindow(r.URL.Query().Get("window"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.EventConsumerExecutionHealthStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("consumer execution health"))
		return
	}
	now := time.Now().UTC()
	out, err := store.GetEventConsumerExecutionHealth(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), now.Add(-window), now)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("subscription ID must be a UUID"))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no matching subscription or retained delivery control")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Consumer execution health timed out", "retry the request"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("consumer execution health"))
	default:
		writeJSON(w, http.StatusOK, out)
	}
}

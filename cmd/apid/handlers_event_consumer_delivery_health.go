package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getEventConsumerDeliveryHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
	store, ok := s.store.(state.EventConsumerDeliveryHealthStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("consumer delivery health"))
		return
	}
	out, err := store.GetEventConsumerDeliveryHealth(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), window)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("subscription ID must be a UUID"))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no matching subscription or retained delivery control")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Consumer delivery health timed out", "retry the request"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("consumer delivery health"))
	default:
		for i, recipient := range out.PendingSample.Recipients {
			out.PendingSample.Recipients[i] = eventBacklogRecipientResponse(recipient)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

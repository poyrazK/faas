package appevents

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) GetEventSubscriptionPendingSummary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionPendingSummaryTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventSubscriptionPendingSummaryStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("subscription pending summary"))
		return
	}
	out, err := store.GetEventSubscriptionPendingSummary(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"))
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("subscription ID must be a UUID"))
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "no matching subscription or retained delivery control")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Pending summary timed out", "coverage is unknown; retry with a fresh observation"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("subscription pending summary"))
	default:
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

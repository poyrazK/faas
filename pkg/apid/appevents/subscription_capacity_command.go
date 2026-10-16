package appevents

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) GetEventSubscriptionCapacityCommand(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventSubscriptionCapacityCommandReadStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("capacity command lookup"))
		return
	}
	out, err := store.GetEventSubscriptionCapacityCommand(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), r.PathValue("commandID"))
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("subscription ID must be a UUID and command ID a nonzero hyphenated UUID"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "event_subscription_capacity_command_unavailable", "Capacity command evidence unavailable", "no retained receipt is available for this owned subscription; absence does not prove that the command never ran"))
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Capacity command lookup timed out", "retry the read-only lookup"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("capacity command lookup"))
	default:
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

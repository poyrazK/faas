package appevents

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) eventSubscriptionControl(w http.ResponseWriter, r *http.Request, acct state.Account, action string) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EventSubscriptionControlStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("subscription delivery controls"))
		return
	}
	out, err := applyEventSubscriptionControl(ctx, store, r, acct.ID, app.ID, action)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("subscription ID must be a UUID and rate must be between 0 and 100"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Subscription delivery control not found", "no matching subscription or retained control belongs to this app"))
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Subscription control timed out", "retry the request"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("subscription delivery controls"))
	default:
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}
func applyEventSubscriptionControl(ctx context.Context, store state.EventSubscriptionControlStore, r *http.Request, account, app, action string) (api.EventSubscriptionDeliveryControl, error) {
	sub := r.PathValue("subscriptionID")
	switch action {
	case "pause":
		return store.SetEventSubscriptionDeliveryControl(ctx, account, app, sub, true, 0)
	case "resume":
		var req api.EventSubscriptionResumeRequest
		if err := httpkit.DecodeJSONSized(r, &req, 4<<10); err != nil {
			return api.EventSubscriptionDeliveryControl{}, state.ErrInvalidArgument
		}
		rate, err := req.Rate()
		if err != nil {
			return api.EventSubscriptionDeliveryControl{}, state.ErrInvalidArgument
		}
		return store.SetEventSubscriptionDeliveryControl(ctx, account, app, sub, false, rate)
	default:
		return store.GetEventSubscriptionDeliveryControl(ctx, account, app, sub)
	}
}
func (s *Service) GetEventSubscriptionDeliveryControl(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.eventSubscriptionControl(w, r, acct, "get")
}
func (s *Service) PauseEventSubscription(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.eventSubscriptionControl(w, r, acct, "pause")
}
func (s *Service) ResumeEventSubscription(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.eventSubscriptionControl(w, r, acct, "resume")
}

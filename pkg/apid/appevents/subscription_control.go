package appevents

import (
	"context"
	"errors"
	"fmt"
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
	if writeSubscriptionCapacityCommandError(w, err) {
		return
	}
	switch {
	case errors.Is(err, state.ErrEventSubscriptionCapacityChanged):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "event_subscription_capacity_changed", "Subscription capacity changed", "saved capacity or plan ceiling differs from the reviewed expectations; refresh preview before applying"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("subscription ID must be a UUID; rate must be between 0 and 100; capacity must be explicit, nonnegative and no greater than the current plan limit; capacity expectations must include both reviewed values"))
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
	case "capacity":
		return applyEventSubscriptionCapacity(ctx, store, r, account, app, sub)
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

func (s *Service) SetEventSubscriptionCapacity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.eventSubscriptionControl(w, r, acct, "capacity")
}

func applyEventSubscriptionCapacity(ctx context.Context, store state.EventSubscriptionControlStore, r *http.Request, account, app, sub string) (api.EventSubscriptionDeliveryControl, error) {
	capacityStore, ok := store.(state.EventSubscriptionCapacityStore)
	if !ok {
		return api.EventSubscriptionDeliveryControl{}, errors.New("subscription capacity store unavailable")
	}
	var req api.EventSubscriptionCapacityRequest
	if err := httpkit.DecodeJSONSized(r, &req, 4<<10); err != nil || req.Validate() != nil {
		return api.EventSubscriptionDeliveryControl{}, state.ErrInvalidArgument
	}
	if req.Expected != nil || req.CommandID != "" {
		guarded, ok := store.(state.EventSubscriptionCapacityGuardedStore)
		if !ok {
			return api.EventSubscriptionDeliveryControl{}, errors.New("guarded subscription capacity store unavailable")
		}
		return guarded.SetEventSubscriptionCapacityGuarded(ctx, account, app, sub, req)
	}
	return capacityStore.SetEventSubscriptionCapacity(ctx, account, app, sub, *req.MaxPendingDeliveries)
}

func writeSubscriptionCapacityCommandError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, state.ErrSubscriptionCapacityCommandConflict):
		api.WriteProblem(w, api.NewProblem(409, "event_subscription_capacity_command_changed", "Capacity command UUID reused", "repeat a command UUID only with its original proposed value and expectations"))
	case errors.Is(err, state.ErrSubscriptionCapacityCommandLimit):
		api.WriteProblem(w, api.NewProblem(429, "event_subscription_capacity_command_limit", "Capacity command receipt limit reached", fmt.Sprintf("the subscription has %d retained commands; known repeats and commands without UUIDs remain available", api.EventSubscriptionCapacityCommandMaxReceipts)).WithLimit(api.EventSubscriptionCapacityCommandMaxReceipts, api.EventSubscriptionCapacityCommandMaxReceipts).WithDocs("https://gregale.dev/docs/event-driven"))
	default:
		return false
	}
	return true
}

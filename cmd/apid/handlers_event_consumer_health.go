package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getEventConsumerHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
	store, ok := s.store.(state.EventConsumerHealthStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("consumer health"))
		return
	}
	now := time.Now().UTC()
	out, err := store.GetEventConsumerHealth(ctx, acct.ID, app.ID, r.PathValue("subscriptionID"), now.Add(-window), now)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("subscription ID must be a UUID"))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no matching subscription or retained delivery control")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Consumer health timed out", "retry the request"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("consumer health"))
	default:
		writeJSON(w, http.StatusOK, out)
	}
}
func validateEventConsumerAlert(metric, sub, window string) *api.Problem {
	if api.IsEventRecoveryAlertMetric(metric) {
		if sub != "" {
			return api.ErrAlertRuleInvalid("recovery health metrics are app-scoped and do not accept event_subscription_id")
		}
		if _, err := api.EventConsumerHealthWindow(window); err != nil {
			return api.ErrAlertRuleInvalid(err.Error())
		}
		return nil
	}
	if !api.IsEventConsumerAlertMetric(metric) {
		if sub != "" {
			return api.ErrAlertRuleInvalid("event_subscription_id requires an event consumer metric")
		}
		return nil
	}
	if _, err := uuid.Parse(sub); err != nil {
		return api.ErrAlertRuleInvalid("event_subscription_id must be a subscription UUID")
	}
	if _, err := api.EventConsumerHealthWindow(window); err != nil {
		return api.ErrAlertRuleInvalid(err.Error())
	}
	return nil
}
func (s *server) checkEventConsumerAlertTarget(w http.ResponseWriter, r *http.Request, account, app string, req api.CreateAlertRuleRequest) bool {
	if !api.IsEventConsumerAlertMetric(req.Metric) {
		return true
	}
	store, ok := s.store.(state.EventSubscriptionControlStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("consumer health alerts"))
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSubscriptionControlTimeout)
	defer cancel()
	_, err := store.GetEventSubscriptionDeliveryControl(ctx, account, app, req.EventSubscriptionID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no matching subscription or retained delivery control")
		return false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("consumer health alerts"))
		return false
	}
	return true
}

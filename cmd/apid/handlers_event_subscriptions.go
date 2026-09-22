package main

import (
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func eventSubscriptionResponse(subscription state.EventSubscription) api.EventSubscriptionResponse {
	return api.EventSubscriptionResponse{
		ID:        subscription.ID,
		AppID:     subscription.AppID,
		Source:    subscription.Source,
		Type:      subscription.Type,
		Filter:    subscription.Filter,
		Enabled:   subscription.Enabled,
		CreatedAt: subscription.CreatedAt,
		UpdatedAt: subscription.UpdatedAt,
	}
}

// listEventSubscriptions exposes the reconciled manifest declarations for an
// app. The app lookup performs the account ownership check before the optional
// event store is touched, preserving the API's cross-account 404 posture.
func (s *server) listEventSubscriptions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	eventStore, ok := s.store.(state.EventSubscriptionStore)
	if !ok {
		slog.Default().Error("event subscription store unavailable", "app_id", app.ID)
		api.WriteProblem(w, api.ErrInternal("event subscriptions"))
		return
	}
	subscriptions, err := eventStore.ListEventSubscriptionsForApp(r.Context(), app.ID)
	if err != nil {
		slog.Default().Error("event subscription list failed", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("event subscriptions"))
		return
	}
	out := api.EventSubscriptionListResponse{
		AppSlug:       app.Slug,
		Subscriptions: make([]api.EventSubscriptionResponse, 0, len(subscriptions)),
	}
	for _, subscription := range subscriptions {
		out.Subscriptions = append(out.Subscriptions, eventSubscriptionResponse(subscription))
	}
	writeJSON(w, http.StatusOK, out)
}

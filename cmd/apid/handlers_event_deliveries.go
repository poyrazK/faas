package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const eventDeliveriesMaxLimit = 200

func eventDeliveryResponse(inv state.Invocation) (api.EventDeliveryResponse, bool) {
	var headers map[string]string
	if len(inv.Headers) == 0 || json.Unmarshal(inv.Headers, &headers) != nil {
		return api.EventDeliveryResponse{}, false
	}
	eventID := strings.TrimSpace(headers["x-gregale-event-id"])
	if eventID == "" {
		return api.EventDeliveryResponse{}, false
	}
	return api.EventDeliveryResponse{
		InvocationID:   inv.ID,
		EventID:        eventID,
		EventSource:    headers["x-gregale-event-source"],
		EventType:      headers["x-gregale-event-type"],
		SubscriptionID: headers["x-gregale-event-subscription-id"],
		State:          string(inv.State),
		Attempts:       inv.Attempts,
		LastError:      inv.LastError,
		CreatedAt:      inv.CreatedAt,
		CompletedAt:    inv.CompletedAt,
	}, true
}

// listEventDeliveries exposes the app-scoped delivery lifecycle without
// making users sift through account-wide invocation history. The app lookup
// preserves the normal cross-account 404 posture.
func (s *server) listEventDeliveries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.EventDeliveryStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event deliveries"))
		return
	}
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), 20, eventDeliveriesMaxLimit, "event deliveries")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	rows, err := store.ListEventDeliveriesForApp(r.Context(), app.ID, limit, r.URL.Query().Get("before"), r.URL.Query().Get("event_id"), r.URL.Query().Get("state"))
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("event deliveries"))
		return
	}
	out := api.EventDeliveryListResponse{AppSlug: app.Slug, Deliveries: make([]api.EventDeliveryResponse, 0, len(rows))}
	for _, row := range rows {
		if delivery, ok := eventDeliveryResponse(row); ok {
			out.Deliveries = append(out.Deliveries, delivery)
		}
	}
	if len(rows) == limit && len(rows) > 0 {
		out.NextBefore = rows[len(rows)-1].ID
	}
	writeJSON(w, http.StatusOK, out)
}

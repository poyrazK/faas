package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const eventDeliveriesMaxLimit = 200

var errInvalidEventFanoutFailureCursor = errors.New("invalid event fanout failure cursor")

type eventFanoutFailureCursor struct {
	Version        int       `json:"v"`
	AppID          string    `json:"app_id"`
	EventID        string    `json:"event_id"`
	CreatedAt      time.Time `json:"created_at"`
	OutboxID       int64     `json:"outbox_id"`
	SubscriptionID string    `json:"subscription_id"`
}

func encodeEventFanoutFailureCursor(appID, eventID string, failure state.EventFanoutFailure) string {
	payload, err := json.Marshal(eventFanoutFailureCursor{
		Version: 1, AppID: appID, EventID: eventID,
		CreatedAt: failure.CreatedAt, OutboxID: failure.OutboxID, SubscriptionID: failure.SubscriptionID,
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeEventFanoutFailureCursor(raw, appID, eventID string) (state.EventFanoutFailureCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return state.EventFanoutFailureCursor{}, nil
	}
	if len(raw) > 1024 {
		return state.EventFanoutFailureCursor{}, errInvalidEventFanoutFailureCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return state.EventFanoutFailureCursor{}, err
	}
	var cursor eventFanoutFailureCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return state.EventFanoutFailureCursor{}, err
	}
	if cursor.Version != 1 || cursor.AppID != appID || cursor.EventID != eventID ||
		cursor.CreatedAt.IsZero() || cursor.OutboxID <= 0 || strings.TrimSpace(cursor.SubscriptionID) == "" {
		return state.EventFanoutFailureCursor{}, errInvalidEventFanoutFailureCursor
	}
	return state.EventFanoutFailureCursor{
		CreatedAt: cursor.CreatedAt, OutboxID: cursor.OutboxID, SubscriptionID: cursor.SubscriptionID,
	}, nil
}

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
	eventID := r.URL.Query().Get("event_id")
	deliveryState := r.URL.Query().Get("state")
	fanoutBefore, err := decodeEventFanoutFailureCursor(r.URL.Query().Get("fanout_before"), app.ID, eventID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid fanout_before", "fanout_before must be an opaque cursor returned as next_fanout_before for this app and event filter"))
		return
	}
	rows, err := store.ListEventDeliveriesForApp(r.Context(), app.ID, limit, r.URL.Query().Get("before"), eventID, deliveryState)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("event deliveries"))
		return
	}
	failureStore, hasFailureHistory := s.store.(state.EventFanoutFailureStore)
	failures := make([]state.EventFanoutFailure, 0)
	if hasFailureHistory && (deliveryState == "" || deliveryState == state.PublishedEventRecipientFailed) {
		failures, err = failureStore.ListEventFanoutFailuresForApp(r.Context(), app.ID, limit+1, fanoutBefore, eventID)
		if err != nil {
			api.WriteProblem(w, api.ErrInternal("event fanout failures"))
			return
		}
	}
	out := api.EventDeliveryListResponse{
		AppSlug: app.Slug, Deliveries: make([]api.EventDeliveryResponse, 0, len(rows)),
		FanoutFailures: make([]api.EventFanoutFailureResponse, 0, len(failures)),
	}
	for _, row := range rows {
		if delivery, ok := eventDeliveryResponse(row); ok {
			out.Deliveries = append(out.Deliveries, delivery)
		}
	}
	if len(rows) == limit && len(rows) > 0 {
		out.NextBefore = rows[len(rows)-1].ID
	}
	if len(failures) > limit {
		out.NextFanoutBefore = encodeEventFanoutFailureCursor(app.ID, eventID, failures[limit-1])
		failures = failures[:limit]
	}
	for _, failure := range failures {
		out.FanoutFailures = append(out.FanoutFailures, api.EventFanoutFailureResponse{
			EventID: failure.EventID, EventSource: failure.EventSource, EventType: failure.EventType,
			SubscriptionID: failure.SubscriptionID, State: state.PublishedEventRecipientFailed,
			Attempts: failure.Attempts, LastError: failure.LastError,
			CreatedAt: failure.CreatedAt, FailedAt: failure.FailedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

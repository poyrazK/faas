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

const eventDeliveryCursorPrefix = "edc1."
const eventFanoutAttemptCursorPrefix = "efac1."

var (
	errInvalidEventDeliveryCursor      = errors.New("invalid event delivery cursor")
	errInvalidEventFanoutFailureCursor = errors.New("invalid event fanout failure cursor")
	errInvalidEventFanoutAttemptCursor = errors.New("invalid event fanout attempt cursor")
)

type eventDeliveryCursor struct {
	Version       int    `json:"v"`
	AppID         string `json:"app_id"`
	EventSource   string `json:"event_source"`
	EventID       string `json:"event_id"`
	DeliveryState string `json:"state"`
	InvocationID  string `json:"invocation_id"`
}

func encodeEventDeliveryCursor(appID, eventSource, eventID, deliveryState, invocationID string) string {
	payload, err := json.Marshal(eventDeliveryCursor{
		Version: 1, AppID: appID, EventSource: eventSource, EventID: eventID,
		DeliveryState: deliveryState, InvocationID: invocationID,
	})
	if err != nil {
		return ""
	}
	return eventDeliveryCursorPrefix + base64.RawURLEncoding.EncodeToString(payload)
}

func decodeEventDeliveryCursor(raw, appID, eventSource, eventID, deliveryState string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !strings.HasPrefix(raw, eventDeliveryCursorPrefix) {
		// Older responses used the invocation ID itself as the cursor. Keep
		// accepting those for unqualified queries; source-filtered cursors must
		// carry the filter identity so they cannot skip across event sources.
		if eventSource != "" {
			return "", errInvalidEventDeliveryCursor
		}
		return raw, nil
	}
	if len(raw) > 2048 {
		return "", errInvalidEventDeliveryCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, eventDeliveryCursorPrefix))
	if err != nil {
		return "", err
	}
	var cursor eventDeliveryCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return "", err
	}
	if cursor.Version != 1 || cursor.AppID != appID || cursor.EventSource != eventSource ||
		cursor.EventID != eventID || cursor.DeliveryState != deliveryState || strings.TrimSpace(cursor.InvocationID) == "" {
		return "", errInvalidEventDeliveryCursor
	}
	return cursor.InvocationID, nil
}

type eventFanoutFailureCursor struct {
	Version        int       `json:"v"`
	AppID          string    `json:"app_id"`
	EventSource    string    `json:"event_source"`
	EventID        string    `json:"event_id"`
	CreatedAt      time.Time `json:"created_at"`
	OutboxID       int64     `json:"outbox_id"`
	SubscriptionID string    `json:"subscription_id"`
}

func encodeEventFanoutFailureCursor(appID, eventSource, eventID string, failure state.EventFanoutFailure) string {
	payload, err := json.Marshal(eventFanoutFailureCursor{
		Version: 1, AppID: appID, EventSource: eventSource, EventID: eventID,
		CreatedAt: failure.CreatedAt, OutboxID: failure.OutboxID, SubscriptionID: failure.SubscriptionID,
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeEventFanoutFailureCursor(raw, appID, eventSource, eventID string) (state.EventFanoutFailureCursor, error) {
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
	if cursor.Version != 1 || cursor.AppID != appID || cursor.EventSource != eventSource || cursor.EventID != eventID ||
		cursor.CreatedAt.IsZero() || cursor.OutboxID <= 0 || strings.TrimSpace(cursor.SubscriptionID) == "" {
		return state.EventFanoutFailureCursor{}, errInvalidEventFanoutFailureCursor
	}
	return state.EventFanoutFailureCursor{
		CreatedAt: cursor.CreatedAt, OutboxID: cursor.OutboxID, SubscriptionID: cursor.SubscriptionID,
	}, nil
}

type eventFanoutAttemptCursor struct {
	Version        int    `json:"v"`
	AppID          string `json:"app_id"`
	EventSource    string `json:"event_source"`
	EventID        string `json:"event_id"`
	SubscriptionID string `json:"subscription_id"`
	AttemptID      int64  `json:"attempt_id"`
}

func encodeEventFanoutAttemptCursor(appID, eventSource, eventID, subscriptionID string, attempt state.EventFanoutAttempt) string {
	payload, err := json.Marshal(eventFanoutAttemptCursor{
		Version: 1, AppID: appID, EventSource: eventSource, EventID: eventID,
		SubscriptionID: subscriptionID, AttemptID: attempt.ID,
	})
	if err != nil {
		return ""
	}
	return eventFanoutAttemptCursorPrefix + base64.RawURLEncoding.EncodeToString(payload)
}

func decodeEventFanoutAttemptCursor(raw, appID, eventSource, eventID, subscriptionID string) (state.EventFanoutAttemptCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return state.EventFanoutAttemptCursor{}, nil
	}
	if len(raw) > 1024 || !strings.HasPrefix(raw, eventFanoutAttemptCursorPrefix) {
		return state.EventFanoutAttemptCursor{}, errInvalidEventFanoutAttemptCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, eventFanoutAttemptCursorPrefix))
	if err != nil {
		return state.EventFanoutAttemptCursor{}, errInvalidEventFanoutAttemptCursor
	}
	var cursor eventFanoutAttemptCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return state.EventFanoutAttemptCursor{}, errInvalidEventFanoutAttemptCursor
	}
	if cursor.Version != 1 || cursor.AppID != appID || cursor.EventSource != eventSource ||
		cursor.EventID != eventID || cursor.SubscriptionID != subscriptionID || cursor.AttemptID <= 0 {
		return state.EventFanoutAttemptCursor{}, errInvalidEventFanoutAttemptCursor
	}
	return state.EventFanoutAttemptCursor{ID: cursor.AttemptID}, nil
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
		InvocationID:     inv.ID,
		InvocationSource: string(inv.Source),
		EventID:          eventID,
		EventSource:      headers["x-gregale-event-source"],
		EventType:        headers["x-gregale-event-type"],
		SubscriptionID:   headers["x-gregale-event-subscription-id"],
		State:            string(inv.State),
		Attempts:         inv.Attempts,
		LastError:        inv.LastError,
		CreatedAt:        inv.CreatedAt,
		CompletedAt:      inv.CompletedAt,
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
	eventID := strings.TrimSpace(r.URL.Query().Get("event_id"))
	eventSource := strings.TrimSpace(r.URL.Query().Get("event_source"))
	deliveryState := r.URL.Query().Get("state")
	if eventSource != "" && eventID == "" {
		api.WriteProblem(w, api.ErrValidation("event_source requires event_id"))
		return
	}
	before, err := decodeEventDeliveryCursor(r.URL.Query().Get("before"), app.ID, eventSource, eventID, deliveryState)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid before cursor", "before must be a cursor returned as next_before for this app and event filters"))
		return
	}
	fanoutBefore, err := decodeEventFanoutFailureCursor(r.URL.Query().Get("fanout_before"), app.ID, eventSource, eventID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid fanout_before", "fanout_before must be an opaque cursor returned as next_fanout_before for this app and event filter"))
		return
	}
	rows, err := store.ListEventDeliveriesForApp(r.Context(), app.ID, limit, before, eventSource, eventID, deliveryState)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("event deliveries"))
		return
	}
	failureStore, hasFailureHistory := s.store.(state.EventFanoutFailureStore)
	failures := make([]state.EventFanoutFailure, 0)
	if hasFailureHistory && (deliveryState == "" || deliveryState == state.PublishedEventRecipientFailed) {
		failures, err = failureStore.ListEventFanoutFailuresForApp(r.Context(), app.ID, limit+1, fanoutBefore, eventSource, eventID)
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
		out.NextBefore = encodeEventDeliveryCursor(app.ID, eventSource, eventID, deliveryState, rows[len(rows)-1].ID)
	}
	if len(failures) > limit {
		out.NextFanoutBefore = encodeEventFanoutFailureCursor(app.ID, eventSource, eventID, failures[limit-1])
		failures = failures[:limit]
	}
	for _, failure := range failures {
		failureCode := failure.FailureCode
		if failureCode == "" {
			failureCode = state.EventFanoutFailureCodeUnknown
		}
		out.FanoutFailures = append(out.FanoutFailures, api.EventFanoutFailureResponse{
			EventID: failure.EventID, EventSource: failure.EventSource, EventType: failure.EventType,
			SubscriptionID: failure.SubscriptionID, State: state.PublishedEventRecipientFailed,
			Attempts: failure.Attempts, FailureCode: failureCode, Retryable: failure.Retryable, LastError: failure.LastError,
			CreatedAt: failure.CreatedAt, FailedAt: failure.FailedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// listEventFanoutAttemptHistory returns the bounded immutable routing
// timeline for one event identity. Requiring both source and ID keeps the
// query specific even when producers reuse IDs across sources.
func (s *server) listEventFanoutAttemptHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	eventSource := strings.TrimSpace(r.URL.Query().Get("event_source"))
	eventID := strings.TrimSpace(r.URL.Query().Get("event_id"))
	subscriptionID := strings.TrimSpace(r.URL.Query().Get("subscription_id"))
	if eventSource == "" || eventID == "" {
		api.WriteProblem(w, api.ErrValidation("event_source and event_id are required"))
		return
	}
	before, err := decodeEventFanoutAttemptCursor(r.URL.Query().Get("before"), app.ID, eventSource, eventID, subscriptionID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid before cursor", "before must be a cursor returned for this app, event identity and subscription filter"))
		return
	}
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), 20, eventDeliveriesMaxLimit, "event fanout attempt history")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	store, ok := s.store.(state.EventFanoutAttemptHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event fanout attempt history"))
		return
	}
	rows, err := store.ListEventFanoutAttemptsForApp(r.Context(), app.ID, limit+1, before,
		eventSource, eventID, subscriptionID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("event fanout attempt history"))
		return
	}
	out := api.EventFanoutAttemptHistoryResponse{
		AppSlug: app.Slug, EventSource: eventSource, EventID: eventID,
		SubscriptionID: subscriptionID, History: make([]api.EventFanoutAttemptResponse, 0, len(rows)),
	}
	if len(rows) > limit {
		out.NextBefore = encodeEventFanoutAttemptCursor(app.ID, eventSource, eventID, subscriptionID, rows[limit-1])
		rows = rows[:limit]
	}
	for _, row := range rows {
		out.History = append(out.History, api.EventFanoutAttemptResponse{
			SubscriptionID: row.SubscriptionID, Action: row.Action, State: row.State,
			AttemptNumber: row.Attempts, FailureCode: row.FailureCode,
			Retryable: row.Retryable, LastError: row.LastError, OccurredAt: row.OccurredAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// replayEventFanoutFailure requeues exactly one terminal recipient from the
// event's immutable acceptance-time snapshot.
func (s *server) replayEventFanoutFailure(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.ReplayEventFanoutFailureRequest
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.EventID) == "" ||
		strings.TrimSpace(req.EventSource) == "" || strings.TrimSpace(req.SubscriptionID) == "" {
		api.WriteProblem(w, api.ErrValidation("event_id, event_source and subscription_id are required"))
		return
	}
	store, ok := s.store.(state.EventFanoutReplayStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event fanout replay"))
		return
	}
	err := store.ReplayFailedPublishedEventRecipientForApp(r.Context(), acct.ID, app.ID,
		req.EventSource, req.EventID, req.SubscriptionID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Event fanout failure not found", "no failed recipient with that event identity belongs to this app"))
		return
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Event fanout failure is not replayable yet", "the event fanout receipt is still being processed; retry after it settles"))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("event fanout replay"))
		return
	}
	writeJSON(w, http.StatusAccepted, api.ReplayEventFanoutFailureResponse{
		EventID: req.EventID, EventSource: req.EventSource, SubscriptionID: req.SubscriptionID,
		State: state.PublishedEventRecipientPending,
	})
}

// replayRetryableEventFanoutFailures requeues a bounded app-scoped batch of
// terminal recipients whose persisted classification marks them retryable.
func (s *server) replayRetryableEventFanoutFailures(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.ReplayRetryableEventFanoutFailuresRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("request must be valid JSON"))
		return
	}
	req.EventSource = strings.TrimSpace(req.EventSource)
	req.EventID = strings.TrimSpace(req.EventID)
	if (req.EventSource == "") != (req.EventID == "") {
		api.WriteProblem(w, api.ErrValidation("event_source and event_id must be provided together"))
		return
	}
	if req.Limit < 0 || req.Limit > state.EventFanoutReplayBatchMax {
		api.WriteProblem(w, api.ErrValidation("limit must be 0 (the default) or between 1 and 100"))
		return
	}
	limit := req.Limit
	if limit == 0 {
		limit = state.EventFanoutReplayBatchMax
	}
	store, ok := s.store.(state.EventFanoutReplayBatchStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event fanout replay batch"))
		return
	}
	result, err := store.ReplayRetryablePublishedEventRecipientsForApp(r.Context(), acct.ID, app.ID, req.EventSource, req.EventID, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("event fanout replay batch"))
		return
	}
	writeJSON(w, http.StatusAccepted, api.ReplayRetryableEventFanoutFailuresResponse{
		AppSlug: app.Slug, ReplayedCount: result.Replayed, HasMore: result.HasMore,
	})
}

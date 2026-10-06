package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

const eventReceiptReplayCursorPrefix = "err1."

type eventReceiptReplayCursor struct {
	Version        int       `json:"v"`
	AccountID      string    `json:"account_id"`
	Source         string    `json:"source"`
	EventID        string    `json:"id"`
	SubscriptionID string    `json:"subscription_id"`
	OutboxID       int64     `json:"outbox_id"`
	CreatedAt      time.Time `json:"created_at"`
	InvocationID   string    `json:"invocation_id"`
}

func eventReceiptReplayURL(source, id, subscriptionID string) string {
	return "/v1/events/receipt/replays?" + url.Values{"source": {source}, "id": {id}, "subscription_id": {subscriptionID}}.Encode()
}

func decodeEventReceiptReplayCursor(raw, accountID, source, id, subscriptionID string) (state.EventReceiptReplayCursor, error) {
	if raw == "" {
		return state.EventReceiptReplayCursor{}, nil
	}
	invalid := errors.New("invalid or mismatched event receipt replay cursor")
	if len(raw) > 8192 || !strings.HasPrefix(raw, eventReceiptReplayCursorPrefix) {
		return state.EventReceiptReplayCursor{}, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, eventReceiptReplayCursorPrefix))
	if err != nil {
		return state.EventReceiptReplayCursor{}, invalid
	}
	var cursor eventReceiptReplayCursor
	if json.Unmarshal(data, &cursor) != nil || cursor.Version != 1 || cursor.AccountID != accountID || cursor.Source != source || cursor.EventID != id || cursor.SubscriptionID != subscriptionID || cursor.OutboxID <= 0 || cursor.CreatedAt.IsZero() {
		return state.EventReceiptReplayCursor{}, invalid
	}
	if _, err := uuid.Parse(cursor.InvocationID); err != nil {
		return state.EventReceiptReplayCursor{}, invalid
	}
	return state.EventReceiptReplayCursor{OutboxID: cursor.OutboxID, CreatedAt: cursor.CreatedAt, InvocationID: cursor.InvocationID}, nil
}

func eventReceiptReplayResponse(accountID string, history state.EventReceiptReplayHistory) api.EventReceiptReplayHistoryResponse {
	out := api.EventReceiptReplayHistoryResponse{EventSource: history.EventSource, EventID: history.EventID, SubscriptionID: history.SubscriptionID, OriginalInvocationID: history.OriginalInvocationID, Replays: make([]api.EventReceiptExecutionResponse, 0, len(history.Replays))}
	for _, replay := range history.Replays {
		out.Replays = append(out.Replays, api.EventReceiptExecutionResponse(replay))
	}
	if history.NextCursor.OutboxID != 0 {
		data, _ := json.Marshal(eventReceiptReplayCursor{Version: 1, AccountID: accountID, Source: history.EventSource, EventID: history.EventID, SubscriptionID: history.SubscriptionID, OutboxID: history.OutboxID, CreatedAt: history.NextCursor.CreatedAt, InvocationID: history.NextCursor.InvocationID})
		out.NextAfter = eventReceiptReplayCursorPrefix + base64.RawURLEncoding.EncodeToString(data)
	}
	return out
}

func (s *server) getEventReceiptReplays(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, id, subscriptionID := strings.TrimSpace(r.URL.Query().Get("source")), strings.TrimSpace(r.URL.Query().Get("id")), strings.TrimSpace(r.URL.Query().Get("subscription_id"))
	for _, value := range []string{source, id, subscriptionID} {
		if value == "" || len(value) > events.EnvelopeStringMax {
			api.WriteProblem(w, api.ErrValidation("source, id and subscription_id are required and must be at most 256 bytes"))
			return
		}
	}
	problem, limit := api.ParseLimit(r.URL.Query().Get("limit"), 100, api.EventReceiptPageMax, "replays")
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	cursor, err := decodeEventReceiptReplayCursor(r.URL.Query().Get("after"), acct.ID, source, id, subscriptionID)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.store.(state.EventReceiptReplayStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event receipt replay store"))
		return
	}
	history, err := store.EventReceiptReplays(r.Context(), acct.ID, source, id, subscriptionID, cursor, limit)
	if err != nil {
		s.writeEventReceiptError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, eventReceiptReplayResponse(acct.ID, history))
}

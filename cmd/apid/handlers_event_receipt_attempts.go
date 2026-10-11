package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

const eventReceiptAttemptCursorPrefix = "era1."

type eventReceiptAttemptCursor struct {
	Version        int    `json:"v"`
	AccountID      string `json:"account_id"`
	Source         string `json:"source"`
	EventID        string `json:"id"`
	SubscriptionID string `json:"subscription_id"`
	OutboxID       int64  `json:"outbox_id"`
	AfterID        int64  `json:"after_id"`
}

func eventReceiptAttemptURL(source, id, subscriptionID string) string {
	return "/v1/events/receipt/attempts?" + url.Values{"source": {source}, "id": {id}, "subscription_id": {subscriptionID}}.Encode()
}

func decodeEventReceiptAttemptCursor(raw, accountID, source, id, subscriptionID string) (state.EventReceiptAttemptCursor, error) {
	if raw == "" {
		return state.EventReceiptAttemptCursor{}, nil
	}
	invalid := errors.New("invalid or mismatched event receipt attempt cursor")
	if len(raw) > 8192 || !strings.HasPrefix(raw, eventReceiptAttemptCursorPrefix) {
		return state.EventReceiptAttemptCursor{}, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, eventReceiptAttemptCursorPrefix))
	if err != nil {
		return state.EventReceiptAttemptCursor{}, invalid
	}
	var cursor eventReceiptAttemptCursor
	if json.Unmarshal(data, &cursor) != nil || cursor.Version != 1 || cursor.AccountID != accountID || cursor.Source != source || cursor.EventID != id || cursor.SubscriptionID != subscriptionID || cursor.OutboxID <= 0 || cursor.AfterID <= 0 {
		return state.EventReceiptAttemptCursor{}, invalid
	}
	return state.EventReceiptAttemptCursor{OutboxID: cursor.OutboxID, AfterID: cursor.AfterID}, nil
}

func eventReceiptAttemptResponse(accountID string, history state.EventReceiptAttemptHistory) api.EventReceiptAttemptHistoryResponse {
	out := api.EventReceiptAttemptHistoryResponse{EventSource: history.EventSource, EventID: history.EventID, SubscriptionID: history.SubscriptionID, OriginalInvocationID: history.OriginalInvocationID, Coverage: "recorded_attempts_only", Attempts: make([]api.InvocationAttemptResponse, 0, len(history.Attempts))}
	for _, attempt := range history.Attempts {
		out.Attempts = append(out.Attempts, api.InvocationAttemptResponse(attempt))
	}
	if history.NextCursor.OutboxID != 0 {
		data, _ := json.Marshal(eventReceiptAttemptCursor{Version: 1, AccountID: accountID, Source: history.EventSource, EventID: history.EventID, SubscriptionID: history.SubscriptionID, OutboxID: history.OutboxID, AfterID: history.NextCursor.AfterID})
		out.NextAfter = eventReceiptAttemptCursorPrefix + base64.RawURLEncoding.EncodeToString(data)
	}
	return out
}

func (s *server) getEventReceiptAttempts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, id, sub := strings.TrimSpace(r.URL.Query().Get("source")), strings.TrimSpace(r.URL.Query().Get("id")), strings.TrimSpace(r.URL.Query().Get("subscription_id"))
	for _, value := range []string{source, id, sub} {
		if value == "" || len(value) > events.EnvelopeStringMax {
			api.WriteProblem(w, api.ErrValidation("source, id and subscription_id are required and must be at most 256 bytes"))
			return
		}
	}
	problem, limit := api.ParseLimit(r.URL.Query().Get("limit"), 100, api.EventReceiptPageMax, "attempts")
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	cursor, err := decodeEventReceiptAttemptCursor(r.URL.Query().Get("after"), acct.ID, source, id, sub)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.store.(state.EventReceiptAttemptStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event receipt attempt store"))
		return
	}
	history, err := store.EventReceiptAttempts(r.Context(), acct.ID, source, id, sub, cursor, limit)
	if err != nil {
		s.writeEventReceiptError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, eventReceiptAttemptResponse(acct.ID, history))
}

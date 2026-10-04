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

const eventReceiptCursorPrefix = "erc1."

type eventReceiptCursor struct {
	Version   int    `json:"v"`
	AccountID string `json:"account_id"`
	Source    string `json:"source"`
	EventID   string `json:"id"`
	OutboxID  int64  `json:"outbox_id"`
	Position  int64  `json:"position"`
}

func eventReceiptURL(source, id string) string {
	return "/v1/events/receipt?" + url.Values{"source": {source}, "id": {id}}.Encode()
}

func decodeEventReceiptCursor(raw, accountID, source, id string) (state.EventReceiptCursor, error) {
	if raw == "" {
		return state.EventReceiptCursor{}, nil
	}
	invalid := errors.New("invalid or mismatched event receipt cursor")
	if len(raw) > 8192 || !strings.HasPrefix(raw, eventReceiptCursorPrefix) {
		return state.EventReceiptCursor{}, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, eventReceiptCursorPrefix))
	if err != nil {
		return state.EventReceiptCursor{}, invalid
	}
	var cursor eventReceiptCursor
	if json.Unmarshal(data, &cursor) != nil || cursor.Version != 1 || cursor.AccountID != accountID || cursor.Source != source || cursor.EventID != id || cursor.OutboxID <= 0 || cursor.Position <= 0 {
		return state.EventReceiptCursor{}, invalid
	}
	return state.EventReceiptCursor{OutboxID: cursor.OutboxID, Position: cursor.Position}, nil
}

func encodeEventReceiptCursor(accountID string, receipt state.EventReceipt) string {
	if receipt.NextPosition == 0 {
		return ""
	}
	data, _ := json.Marshal(eventReceiptCursor{Version: 1, AccountID: accountID, Source: receipt.EventSource, EventID: receipt.EventID, OutboxID: receipt.OutboxID, Position: receipt.NextPosition})
	return eventReceiptCursorPrefix + base64.RawURLEncoding.EncodeToString(data)
}

// getEventReceipt reads a repeatable snapshot; it neither claims nor replays work.
func (s *server) getEventReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, id := strings.TrimSpace(r.URL.Query().Get("source")), strings.TrimSpace(r.URL.Query().Get("id"))
	if source == "" || id == "" || len(source) > events.EnvelopeStringMax || len(id) > events.EnvelopeStringMax {
		api.WriteProblem(w, api.ErrValidation("source and id are required and must be at most 256 bytes"))
		return
	}
	problem, limit := api.ParseLimit(r.URL.Query().Get("limit"), 100, api.EventReceiptPageMax, "recipients")
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	cursor, err := decodeEventReceiptCursor(r.URL.Query().Get("after"), acct.ID, source, id)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.store.(state.EventReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event receipt store"))
		return
	}
	receipt, err := store.EventReceipt(r.Context(), acct.ID, source, id, cursor, limit)
	if err != nil {
		s.writeEventReceiptError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, eventReceiptResponse(acct.ID, receipt))
}

func (s *server) writeEventReceiptError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Event receipt not found", "No retained receipt exists for this source and id"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.ErrValidation("event receipt cursor is stale; restart without after"))
	default:
		s.log.ErrorContext(r.Context(), "read event receipt", "err", err)
		api.WriteProblem(w, api.ErrInternal("read event receipt"))
	}
}

func eventReceiptResponse(accountID string, receipt state.EventReceipt) api.EventReceiptResponse {
	out := api.EventReceiptResponse{EventID: receipt.EventID, EventSource: receipt.EventSource, EventType: receipt.EventType,
		SchemaVersion: receipt.SchemaVersion, AcceptedAt: receipt.AcceptedAt, RoutingSettledAt: receipt.RoutingSettledAt, RetainUntil: receipt.RetainUntil,
		SnapshotCaptured: receipt.SnapshotCaptured, RoutingMode: "event", RecipientCount: receipt.RecipientCount, RoutingSummary: receipt.RoutingSummary,
		Recipients: make([]api.EventReceiptRecipientResponse, 0, len(receipt.Recipients)), NextAfter: encodeEventReceiptCursor(accountID, receipt)}
	if receipt.RecipientClaims {
		out.RoutingMode = "recipient"
	}
	for _, entry := range receipt.Recipients {
		recipient := api.EventReceiptRecipientResponse{SubscriptionID: entry.SubscriptionID, AppID: entry.AppID, AppSlug: entry.AppSlug,
			Routing: api.EventReceiptRoutingResponse(entry.Routing), ExecutionUnavailable: entry.ExecutionUnavailable, RecoveryActions: eventReceiptActions(receipt, entry)}
		if entry.Execution != nil {
			execution := api.EventReceiptExecutionResponse(*entry.Execution)
			recipient.Execution = &execution
		}
		if entry.Recovery != nil {
			latest := api.EventReceiptExecutionResponse(*entry.Recovery.LatestReplay)
			recipient.Recovery = &api.EventReceiptRecoveryResponse{RetainedReplayCount: entry.Recovery.RetainedReplayCount, LatestReplay: &latest,
				HistoryURL: eventReceiptReplayURL(receipt.EventSource, receipt.EventID, entry.SubscriptionID)}
		}
		if entry.Cancellation != nil {
			cancellation := api.EventReceiptCancellationResponse(*entry.Cancellation)
			recipient.Cancellation = &cancellation
		}
		if entry.AppSlug != "" {
			query := url.Values{"event_source": {receipt.EventSource}, "event_id": {receipt.EventID}, "subscription_id": {entry.SubscriptionID}}
			recipient.FanoutHistoryURL = "/v1/apps/" + url.PathEscape(entry.AppSlug) + "/event-deliveries/attempts?" + query.Encode()
		}
		out.Recipients = append(out.Recipients, recipient)
	}
	return out
}

func eventReceiptActions(receipt state.EventReceipt, entry state.EventReceiptRecipient) []api.EventReceiptRecoveryAction {
	actions := make([]api.EventReceiptRecoveryAction, 0, 1)
	if entry.RoutingReplayEligible {
		actions = append(actions, api.EventReceiptRecoveryAction{Kind: "routing_replay", Method: http.MethodPost,
			URL:  "/v1/apps/" + url.PathEscape(entry.AppSlug) + "/event-deliveries:replay-fanout-failure",
			Body: &api.ReplayEventFanoutFailureRequest{EventSource: receipt.EventSource, EventID: receipt.EventID, SubscriptionID: entry.SubscriptionID}})
	}
	if entry.HandlerReplayMode != "" {
		id := entry.HandlerReplayInvocationID
		if id == "" && entry.Execution != nil {
			id = entry.Execution.InvocationID
		}
		path := "/v1/invocations/" + url.PathEscape(id) + "/replay"
		if entry.HandlerReplayMode == "dead_letter_replay" {
			path = "/v1/apps/" + url.PathEscape(entry.AppSlug) + "/queues/dead_letter/" + url.PathEscape(id) + "/replay"
		}
		actions = append(actions, api.EventReceiptRecoveryAction{Kind: entry.HandlerReplayMode, Method: http.MethodPost, URL: path})
	}
	return actions
}

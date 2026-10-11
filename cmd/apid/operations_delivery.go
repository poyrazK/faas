package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) ownedOperationDelivery(w http.ResponseWriter, r *http.Request, acct state.Account) (api.OperationDeliveryInspection, bool) {
	op, _, ok := s.ownedOperation(w, r, acct)
	if !ok {
		return api.OperationDeliveryInspection{}, false
	}
	store, ok := s.store.(state.OperationDeliveryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation delivery storage is unavailable"))
		return api.OperationDeliveryInspection{}, false
	}
	report, err := store.OperationDeliverySnapshot(r.Context(), acct.ID, op.ID)
	if err != nil {
		writeOperationError(w, err)
		return report, false
	}
	return report, true
}

func (s *server) getOperationDelivery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if r.URL.RawQuery != "" {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	report, ok := s.ownedOperationDelivery(w, r, acct)
	if !ok {
		return
	}
	if report.WebhookID != "" {
		health, err := s.store.AppWebhookDeliveryHealth(r.Context(), report.WebhookID, acct.ID, report.ObservedAt)
		if err == nil {
			report.ReceiverState = string(health.ReceiverState)
			report.ReceiverCooldownUntil = health.ReceiverCooldownUntil
		} else {
			report.ReceiverState = "unavailable"
		}
	}
	writeJSON(w, http.StatusOK, report)
}

type operationDeliveryCursor struct {
	OperationID string `json:"operation_id"`
	DeliveryID  string `json:"delivery_id"`
	After       string `json:"after"`
}

func operationDeliveryAttemptQuery(r *http.Request, report api.OperationDeliveryInspection) (int, string, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, "", state.ErrInvalidArgument
	}
	for key, values := range q {
		if (key != "limit" && key != "cursor") || len(values) != 1 || values[0] == "" {
			return 0, "", state.ErrInvalidArgument
		}
	}
	limit := api.OperationHistoryPageDefault
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > api.OperationHistoryPageMax {
			return 0, "", state.ErrInvalidArgument
		}
	}
	cursor := q.Get("cursor")
	if cursor == "" {
		return limit, "", nil
	}
	if len(cursor) > api.OperationHistoryCursorMaxBytes {
		return 0, "", state.ErrInvalidArgument
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, "", state.ErrInvalidArgument
	}
	var c operationDeliveryCursor
	if json.Unmarshal(raw, &c) != nil || c.OperationID != report.OperationID || c.DeliveryID != report.DeliveryID || c.After == "" {
		return 0, "", state.ErrInvalidArgument
	}
	canonical, _ := json.Marshal(c)
	if base64.RawURLEncoding.EncodeToString(canonical) != cursor {
		return 0, "", state.ErrInvalidArgument
	}
	return limit, c.After, nil
}

func (s *server) getOperationDeliveryAttempts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	report, ok := s.ownedOperationDelivery(w, r, acct)
	if !ok {
		return
	}
	limit, cursor, err := operationDeliveryAttemptQuery(r, report)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	if report.WebhookID == "" {
		writeOperationError(w, state.ErrNotFound)
		return
	}
	rows, next, err := s.store.ListAppWebhookDeliveryAttempts(r.Context(), report.DeliveryID, report.WebhookID, acct.ID, limit, cursor)
	if err != nil {
		if errors.Is(err, state.ErrInvalidAppWebhookAttemptPageToken) {
			err = state.ErrInvalidArgument
		}
		writeOperationError(w, err)
		return
	}
	page := api.OperationDeliveryAttemptsResponse{OperationID: report.OperationID, DeliveryID: report.DeliveryID, Attempts: []api.OperationDeliveryAttempt{}}
	for _, a := range rows {
		page.Attempts = append(page.Attempts, api.OperationDeliveryAttempt{ReplayGeneration: a.ReplayGeneration, AttemptNumber: a.AttemptNumber, Outcome: a.Outcome, ResponseCode: a.ResponseCode, ErrorCode: state.OperationDeliveryErrorCode(a.ResponseCode, a.Error), StartedAt: a.StartedAt, FinishedAt: a.FinishedAt, NextAttemptAt: a.NextAttemptAt})
	}
	if next != "" {
		raw, _ := json.Marshal(operationDeliveryCursor{report.OperationID, report.DeliveryID, next})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) retryOperationDeliveryWithReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if r.URL.RawQuery != "" {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	op, _, ok := s.ownedOperation(w, r, acct)
	if !ok {
		return
	}
	var req api.OperationDeliveryRetryRequest
	if !decodeOperationBody(w, r, &req, api.OperationDeliveryRetryMaxBytes) {
		return
	}
	store, ok := s.store.(state.OperationDeliveryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation delivery storage is unavailable"))
		return
	}
	receipt, err := store.RetryOperationCompletionDelivery(r.Context(), acct.ID, op.ID, req)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "app.operation_delivery_retry_decision", &acct.ID, map[string]any{"operation_id": op.ID, "retry_id": receipt.RetryID, "delivery_id": receipt.DeliveryID, "replay_generation": receipt.ReplayGeneration})
	writeJSON(w, http.StatusOK, receipt)
}

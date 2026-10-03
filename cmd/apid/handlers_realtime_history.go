package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getManagedRealtimeHistoryUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.realtimeHistoryPreviewEnabled {
		s.notFound(w, "managed realtime history usage unavailable")
		return
	}
	reader, ok := s.store.(state.ManagedRealtimeHistoryUsageReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime history usage unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	usage, err := reader.ReadManagedRealtimeHistoryUsage(ctx, acct.ID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "managed realtime history usage read failed", "err", err)
		api.WriteProblem(w, api.ErrCapacity("managed realtime history usage unavailable"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, api.ManagedRealtimeHistoryUsageResponse{
		ObservedAt:    usage.ObservedAt.Format(time.RFC3339Nano),
		EndpointCount: usage.EndpointCount, ChannelCount: usage.ChannelCount,
		StoredMessageCount: usage.StoredMessageCount, StoredPayloadBytes: usage.StoredPayloadBytes,
		ReplayableMessageCount: usage.ReplayableMessageCount, ReplayablePayloadBytes: usage.ReplayablePayloadBytes,
	})
}

func (s *server) managedRealtimeHistoryStore(w http.ResponseWriter) (state.ManagedRealtimeHistoryStore, bool) {
	store, ok := s.store.(state.ManagedRealtimeHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime history store unavailable"))
	}
	return store, ok
}

func retainedMessageResponse(message state.ManagedRealtimeChannelMessage) api.ManagedRealtimeRetainedMessageResponse {
	return api.ManagedRealtimeRetainedMessageResponse{
		Sequence: message.Sequence, DataBase64: base64.StdEncoding.EncodeToString(message.Data),
		Binary: message.Binary, CreatedAt: message.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"),
	}
}

func (s *server) appendManagedRealtimeRetainedMessage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.realtimeHistoryPreviewEnabled {
		s.notFound(w, "retained realtime messages unavailable")
		return
	}
	row, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	channel := r.PathValue("channel")
	if problem := validateManagedRealtimeChannel(channel); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	store, ok := s.managedRealtimeHistoryStore(w)
	if !ok {
		return
	}
	var request api.ManagedRealtimeRetainedMessageRequest
	if err := decodeJSONSized(r, &request, 8<<10); err != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
		return
	}
	data, err := base64.StdEncoding.DecodeString(request.DataBase64)
	if err != nil || len(data) > state.ManagedRealtimeHistoryMaxPayloadBytes || len(request.IdempotencyKey) > 128 {
		api.WriteProblem(w, api.ErrRealtimeInvalid("retained message requires valid base64 data of at most 4096 bytes and an idempotency key of at most 128 bytes"))
		return
	}
	message, err := store.AppendManagedRealtimeChannelMessage(r.Context(), row.ID, channel, data, request.Binary, request.IdempotencyKey)
	if err != nil {
		s.writeManagedRealtimeHistoryError(w, r, err)
		return
	}
	s.audit.Emit(r.Context(), "realtime.retained_message_appended", &acct.ID, map[string]any{
		"endpoint_id": row.ID, "channel": channel, "sequence": message.Sequence,
	})
	writeJSON(w, http.StatusCreated, retainedMessageResponse(message))
}

func (s *server) readManagedRealtimeRetainedMessages(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.realtimeHistoryPreviewEnabled {
		s.notFound(w, "retained realtime messages unavailable")
		return
	}
	row, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	channel := r.PathValue("channel")
	if problem := validateManagedRealtimeChannel(channel); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	store, ok := s.managedRealtimeHistoryStore(w)
	if !ok {
		return
	}
	after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if err != nil || after < 0 {
		api.WriteProblem(w, api.ErrRealtimeInvalid("after must be a non-negative channel sequence"))
		return
	}
	problem, limit := api.ParseLimit(r.URL.Query().Get("limit"), 100, state.ManagedRealtimeHistoryMaxRead, "retained messages")
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	history, err := store.ReadManagedRealtimeChannelHistory(r.Context(), row.ID, channel, after, limit)
	if err != nil {
		s.writeManagedRealtimeHistoryError(w, r, err)
		return
	}
	if history.HistoryUnavailable {
		api.WriteProblem(w, api.NewProblem(http.StatusGone, "history_unavailable", "Realtime history unavailable",
			fmt.Sprintf("cursor %d is older than retained history; oldest available sequence is %d; resynchronize before subscribing", after, history.OldestSequence)))
		return
	}
	response := api.ManagedRealtimeRetainedHistoryResponse{
		Messages:       make([]api.ManagedRealtimeRetainedMessageResponse, 0, len(history.Messages)),
		OldestSequence: history.OldestSequence, LatestSequence: history.LatestSequence,
	}
	for _, message := range history.Messages {
		response.Messages = append(response.Messages, retainedMessageResponse(message))
	}
	last := after
	if n := len(history.Messages); n > 0 {
		last = history.Messages[n-1].Sequence
	}
	response.HasMore = last < history.LatestSequence
	writeJSON(w, http.StatusOK, response)
}

func (s *server) writeManagedRealtimeHistoryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, state.ErrManagedRealtimeHistoryInvalid):
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid channel, cursor, payload, or idempotency key"))
	case errors.Is(err, state.ErrManagedRealtimeHistoryLimit):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Realtime channel limit reached", "a realtime endpoint can retain history for at most 32 channels"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Idempotency key conflict", "the retained message key already has a different payload"))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "managed realtime endpoint not found")
	default:
		s.log.ErrorContext(r.Context(), "managed realtime history operation failed", "err", err)
		api.WriteProblem(w, api.ErrCapacity("managed realtime history unavailable"))
	}
}

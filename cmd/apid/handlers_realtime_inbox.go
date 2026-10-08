package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) sendManagedRealtimePrincipalInbox(w http.ResponseWriter, r *http.Request, acct state.Account, row state.ManagedRealtimeEndpoint, request api.ManagedRealtimePrincipalMessageRequest, data []byte) {
	if !s.realtimeHistoryPreviewEnabled {
		s.notFound(w, "realtime inbox preview unavailable")
		return
	}
	if !row.Enabled {
		api.WriteProblem(w, api.ErrRealtimeInvalid("endpoint must be enabled for inbox sends"))
		return
	}
	store, ok := s.store.(state.ManagedRealtimeInboxStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("realtime inbox storage unavailable"))
		return
	}
	var message state.ManagedRealtimeChannelMessage
	var err error
	if request.FallbackAfterSeconds > 0 {
		category := request.NotificationCategory
		if category == "" {
			category = "notifications"
		}
		notifications, available := s.store.(state.ManagedRealtimeCategorizedNotificationStore)
		if !available {
			api.WriteProblem(w, api.ErrCapacity("realtime notification storage unavailable"))
			return
		}
		message, err = notifications.AppendManagedRealtimeCategorizedNotification(r.Context(), row.ID, request.Principal, data, request.Binary, request.MessageID, request.FallbackAfterSeconds, category, request.NotificationGroupKey, request.NotificationGroupLabel, request.NotificationPriority, strconv.Itoa(request.NotificationTTLSeconds), request.NotificationCollapseKey, request.NotificationNotBefore)
	} else {
		message, err = store.AppendManagedRealtimeInboxMessage(r.Context(), row.ID, request.Principal, data, request.Binary, request.MessageID)
	}
	if err != nil {
		s.writeRealtimeInboxError(w, r, err)
		return
	}
	if publisher, ok := s.realtimeOwner.(realtimePrincipalPublisher); ok {
		wakeCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		// A wake never sends payload bytes. Pumps read the committed ordered
		// stream so a lost notification cannot create a delivery gap.
		_, wakeErr := publisher.SendToPrincipal(wakeCtx, row.ID, request, realtime.Message{})
		cancel()
		if wakeErr != nil {
			s.log.DebugContext(r.Context(), "realtime inbox wake deferred to polling", "endpoint_id", row.ID)
		}
	}
	s.audit.Emit(r.Context(), "realtime.inbox_message_retained", &acct.ID, map[string]any{"endpoint_id": row.ID, "message_id": request.MessageID, "sequence": message.Sequence})
	// Realtime nodes read the committed stream. Storage acceptance is sufficient
	// even when every device or realtime node is currently offline.
	response := api.ManagedRealtimePrincipalSendResponse{MessageID: request.MessageID, Sequence: message.Sequence, Durable: true}
	if message.FallbackAfterSeconds > 0 {
		response.FallbackDeadline = message.CreatedAt.Add(time.Duration(message.FallbackAfterSeconds) * time.Second).UTC().Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (s *server) readManagedRealtimeInbox(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.realtimeHistoryPreviewEnabled {
		s.notFound(w, "realtime inbox preview unavailable")
		return
	}
	row, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	principal := r.URL.Query().Get("principal")
	if err := api.ValidateRealtimePrincipal(principal); err != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
		return
	}
	store, ok := s.store.(state.ManagedRealtimeInboxStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("realtime inbox storage unavailable"))
		return
	}
	response := api.ManagedRealtimeInboxResponse{Messages: make([]api.ManagedRealtimeInboxMessageResponse, 0), Consumer: r.URL.Query().Get("consumer")}
	after := int64(0)
	if response.Consumer != "" {
		checkpoint, err := store.GetManagedRealtimeInboxCursor(r.Context(), row.ID, principal, response.Consumer)
		if err != nil {
			s.writeRealtimeInboxError(w, r, err)
			return
		}
		response.AcknowledgedSequence = &checkpoint
		after = checkpoint
	}
	if raw := r.URL.Query().Get("after"); raw != "" {
		var err error
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 {
			api.WriteProblem(w, api.ErrRealtimeInvalid("after must be a non-negative inbox sequence"))
			return
		}
	}
	problem, limit := api.ParseLimit(r.URL.Query().Get("limit"), 100, state.ManagedRealtimeHistoryMaxRead, "inbox messages")
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	history, err := store.ReadManagedRealtimeInbox(r.Context(), row.ID, principal, after, limit)
	if err != nil {
		s.writeRealtimeInboxError(w, r, err)
		return
	}
	response.OldestSequence, response.LatestSequence = history.OldestSequence, history.LatestSequence
	response.HistoryUnavailable = history.HistoryUnavailable
	last := after
	for _, message := range history.Messages {
		response.Messages = append(response.Messages, api.ManagedRealtimeInboxMessageResponse{MessageID: message.TargetMessageID, TargetMessageID: message.TargetMessageID, Version: message.Version, Event: message.MessageEvent, Deleted: message.Deleted, Sequence: message.Sequence,
			DataBase64: base64.StdEncoding.EncodeToString(message.Data), Binary: message.Binary, CreatedAt: message.CreatedAt.UTC().Format(time.RFC3339Nano),
			ExpiresAt: message.CreatedAt.Add(state.ManagedRealtimeInboxRetention).UTC().Format(time.RFC3339Nano)})
		last = message.Sequence
	}
	response.HasMore = !history.HistoryUnavailable && last < history.LatestSequence
	writeJSON(w, http.StatusOK, response)
}

func (s *server) writeRealtimeInboxError(w http.ResponseWriter, r *http.Request, err error) {
	var reducerError *state.ManagedRealtimeReducerError
	if errors.As(err, &reducerError) {
		api.WriteProblem(w, api.ErrRealtimeInvalid(reducerError.Error()))
		return
	}
	if errors.Is(err, state.ErrManagedRealtimeReducerActive) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Reducer manages channel state", "publish compensating reducer events or disable the reducer before editing events or snapshots"))
		return
	}
	var schemaError *state.ManagedRealtimeEventSchemaError
	if errors.As(err, &schemaError) {
		api.WriteProblem(w, api.ErrRealtimeInvalid(schemaError.Error()))
		return
	}
	switch {
	case errors.Is(err, state.ErrManagedRealtimeFallbackSubscription):
		api.WriteProblem(w, api.ErrRealtimeInvalid("configure an enabled push provider or app webhook for realtime.inbox.fallback_required before requesting a fallback"))
	case errors.Is(err, state.ErrManagedRealtimeHistoryInvalid), errors.Is(err, state.ErrManagedRealtimeDurableCursorInvalid):
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid inbox principal, consumer, cursor, payload, or message ID"))
	case errors.Is(err, state.ErrManagedRealtimeHistoryLimit), errors.Is(err, state.ErrManagedRealtimeDurableCursorLimit):
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeCapacity, "Realtime inbox capacity reached", "the endpoint has reached its inbox or device checkpoint limit"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Message ID conflict", "the message version is stale, deleted, or the ID already has different content or policy"))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "realtime inbox endpoint or device checkpoint not found")
	default:
		s.log.WarnContext(r.Context(), "managed realtime inbox operation failed", "err", err)
		api.WriteProblem(w, api.ErrCapacity("realtime inbox unavailable"))
	}
}

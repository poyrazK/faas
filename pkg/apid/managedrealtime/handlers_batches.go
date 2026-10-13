package managedrealtime

import (
	"encoding/base64"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) PublishManagedRealtimeChannelBatch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.HistoryPreviewEnabled {
		httpkit.NotFound(w, "atomic batch publishing preview unavailable")
		return
	}
	ep, owner, ok := s.managedRealtimeEndpointAction(w, r, acct)
	if !ok {
		return
	}
	ch := r.PathValue("channel")
	if problem := validateManagedRealtimeChannel(ch); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var req api.ManagedRealtimeChannelBatchRequest
	if httpkit.DecodeJSONSized(r, &req, 100<<10) != nil || len(req.Messages) < 1 || len(req.Messages) > state.ManagedRealtimeBatchMaxMessages {
		api.WriteProblem(w, api.ErrRealtimeInvalid("batch requires 1..32 messages"))
		return
	}
	if req.TTLSeconds < 0 || req.TTLSeconds > api.RealtimeMessageTTLMaxSeconds {
		api.WriteProblem(w, api.ErrRealtimeInvalid("batch ttl_seconds must be between zero and 86400"))
		return
	}
	items := make([]state.ManagedRealtimeBatchItem, len(req.Messages))
	for i, msg := range req.Messages {
		if msg.ExpectedSequence != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("expected_sequence belongs on the batch, not individual messages"))
			return
		}
		data, err := base64.StdEncoding.DecodeString(msg.DataBase64)
		if err != nil || len(data) > 4096 || ep.MaxMessageBytes > 0 && int64(len(data)) > ep.MaxMessageBytes {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid batch payload or endpoint size limit"))
			return
		}
		ttl := req.TTLSeconds
		if msg.TTLSeconds != nil {
			ttl = *msg.TTLSeconds
		}
		if ttl < 0 || ttl > api.RealtimeMessageTTLMaxSeconds {
			api.WriteProblem(w, api.ErrRealtimeInvalid("message ttl_seconds must be between zero and 86400"))
			return
		}
		items[i] = state.ManagedRealtimeBatchItem{TTLSeconds: ttl, Metadata: msg.Metadata, Data: data, Binary: msg.Binary}
	}
	store, ok := s.Host.Store().(state.ManagedRealtimeBatchStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("batch publishing unavailable"))
		return
	}
	var messages []state.ManagedRealtimeChannelMessage
	var err error
	if req.ExpectedSequence != nil {
		if conditional, ok := s.Host.Store().(state.ManagedRealtimeConditionalStore); ok {
			messages, err = conditional.AppendManagedRealtimeBatchConditional(r.Context(), ep.ID, ch, req.BatchID, items, req.ExpectedSequence)
		} else {
			err = state.ErrManagedRealtimeHistoryInvalid
		}
	} else {
		messages, err = store.AppendManagedRealtimeChannelBatch(r.Context(), ep.ID, ch, req.BatchID, items)
	}
	if errors.Is(err, state.ErrManagedRealtimeBatchLimit) {
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeCapacity, "Batch limit reached", "an endpoint can retain 256 batch IDs for 24 hours"))
		return
	}
	if err != nil {
		s.writeManagedRealtimeHistoryError(w, r, err)
		return
	}
	result := api.ManagedRealtimeChannelBatchResponse{BatchID: req.BatchID, Durable: true, Messages: make([]api.ManagedRealtimePublishResponse, 0, len(messages))}
	for _, msg := range messages {
		outcome := api.ManagedRealtimePublishResponse{Sequence: msg.Sequence, Durable: true}
		payload := realtime.Message{ExpiresAt: msg.ExpiresAt, Data: msg.Data, Binary: msg.Binary}
		if publisher, ok := owner.(realtimeRetainedPublishStatus); ok {
			outcome, err = publisher.PublishRetainedWithStatus(r.Context(), ep.ID, ch, payload, msg.Sequence)
		} else {
			outcome.Partial = true
		}
		outcome.Sequence = msg.Sequence
		outcome.Durable = true
		outcome.Partial = outcome.Partial || err != nil || outcome.NodesUnavailable > 0 || outcome.QueueFull > 0 || outcome.Failed > 0
		result.Partial = result.Partial || outcome.Partial
		result.Messages = append(result.Messages, outcome)
	}
	s.Host.EmitAudit(r.Context(), "realtime.channel_batch_published", &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": ch, "messages": len(messages), "first_sequence": messages[0].Sequence, "partial": result.Partial})
	httpkit.WriteJSON(w, http.StatusOK, result)
}

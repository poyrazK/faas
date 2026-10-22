// adr: 20261022191742
package gateway

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// rehearsalRealtimeBatchBodyMaxBytes is apid's publish-batch body cap.
const rehearsalRealtimeBatchBodyMaxBytes = 100 << 10

func realtimePublishConflict() *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Idempotency key conflict", "this Idempotency-Key was already used with a different realtime message")
}

// realtimeBatchRoute captures POST .../channels/{channel}/publish-batch as one
// retained publish of its messages. The batch ID is its idempotency key.
func realtimeBatchRoute(slug, endpoint, channel string) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		if problem := realtimeChannelProblem(channel); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		body, problem := readRehearsalPlatformBody(w, r, rehearsalRealtimeBatchBodyMaxBytes, call.now)
		var req api.ManagedRealtimeChannelBatchRequest
		if problem != nil || decodeRehearsalStrictJSON(body, &req) != nil || len(req.Messages) < 1 || len(req.Messages) > state.ManagedRealtimeBatchMaxMessages {
			api.WriteProblem(w, api.ErrRealtimeInvalid("batch requires 1..32 messages"))
			return
		}
		if req.TTLSeconds < 0 || req.TTLSeconds > api.RealtimeMessageTTLMaxSeconds {
			api.WriteProblem(w, api.ErrRealtimeInvalid("batch ttl_seconds must be between zero and 86400"))
			return
		}
		messages, problem := rehearsalRealtimeBatchMessages(req)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		fields := map[string]json.RawMessage{"delivery": json.RawMessage(`"retained"`), "messages": messages}
		if req.ExpectedSequence != nil {
			fields["expected_sequence"], _ = json.Marshal(*req.ExpectedSequence)
		}
		capture := state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindRealtimePublish, Address: slug, Endpoint: endpoint, Name: channel, IdempotencyKey: req.BatchID}
		capture.Message, capture.Oversized = rehearsalPlatformMessage(fields, len(messages))
		result, problem := captureRehearsalPlatformCall(r, call, capture, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "managed realtime endpoint not found"),
			api.NewProblem(http.StatusConflict, api.CodeConflict, "Idempotency key conflict", "the retained message key already has a different payload"))
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		if result.Replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		// Nothing is retained or delivered in a rehearsal: no message gets a
		// history sequence.
		out := api.ManagedRealtimeChannelBatchResponse{BatchID: req.BatchID, Durable: true, Messages: make([]api.ManagedRealtimePublishResponse, len(req.Messages))}
		for i := range out.Messages {
			out.Messages[i] = api.ManagedRealtimePublishResponse{NodesQueried: 1, Durable: true}
		}
		writeRehearsalCaptured(w, http.StatusOK, out)
	}
}

// rehearsalRealtimeBatchMessages validates a batch as apid and the history
// store do and encodes its messages with each one's effective TTL.
func rehearsalRealtimeBatchMessages(req api.ManagedRealtimeChannelBatchRequest) (json.RawMessage, *api.Problem) {
	if req.BatchID == "" || len(req.BatchID) > 128 || strings.TrimSpace(req.BatchID) != req.BatchID || strings.ContainsAny(req.BatchID, "\x00\r\n") ||
		req.ExpectedSequence != nil && *req.ExpectedSequence < 0 {
		return nil, api.ErrRealtimeInvalid("invalid channel, cursor, payload, or idempotency key")
	}
	type message struct {
		DataBase64 string            `json:"data_base64"`
		Binary     bool              `json:"binary"`
		Metadata   map[string]string `json:"metadata"`
		TTLSeconds int               `json:"ttl_seconds"`
	}
	out := make([]message, len(req.Messages))
	total := 0
	for i, msg := range req.Messages {
		if msg.ExpectedSequence != nil {
			return nil, api.ErrRealtimeInvalid("expected_sequence belongs on the batch, not individual messages")
		}
		data, err := base64.StdEncoding.DecodeString(msg.DataBase64)
		if err != nil || len(data) > state.ManagedRealtimeHistoryMaxPayloadBytes {
			return nil, api.ErrRealtimeInvalid("invalid batch payload or endpoint size limit")
		}
		ttl := req.TTLSeconds
		if msg.TTLSeconds != nil {
			ttl = *msg.TTLSeconds
		}
		if ttl < 0 || ttl > api.RealtimeMessageTTLMaxSeconds {
			return nil, api.ErrRealtimeInvalid("message ttl_seconds must be between zero and 86400")
		}
		if api.ValidateRealtimeMetadata(msg.Metadata) != nil {
			return nil, api.ErrRealtimeInvalid("invalid channel, cursor, payload, or idempotency key")
		}
		total += len(data)
		out[i] = message{DataBase64: msg.DataBase64, Binary: msg.Binary, Metadata: msg.Metadata, TTLSeconds: ttl}
	}
	if total > state.ManagedRealtimeBatchMaxBytes {
		return nil, api.ErrRealtimeInvalid("invalid channel, cursor, payload, or idempotency key")
	}
	encoded, _ := json.Marshal(out)
	return encoded, nil
}

// realtimePublisherRoute captures POST
// /v1/realtime/publishers/endpoints/{endpoint_id}/channels/{channel}/publish,
// a retained publish with a publisher credential. It names no app, so the
// endpoint alone resolves its target in the run's account.
func realtimePublisherRoute(path string) (rehearsalPlatformRoute, bool) {
	rest, found := strings.CutPrefix(path, "/v1/realtime/publishers/endpoints/")
	if !found {
		return nil, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 4 || parts[0] == "" || parts[1] != "channels" || parts[2] == "" || parts[3] != "publish" {
		return nil, false
	}
	endpoint, channel := parts[0], parts[2]
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		if problem := realtimeChannelProblem(channel); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			api.WriteProblem(w, api.ErrRealtimeInvalid("exactly one Idempotency-Key is required"))
			return
		}
		body, problem := readRehearsalPlatformBody(w, r, api.RealtimePublisherBodyMaxBytes, call.now)
		var req state.RealtimePublisherMessageRequest
		if problem != nil || decodeRehearsalStrictJSON(body, &req) != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid publish payload"))
			return
		}
		metadata, ok := state.ValidRealtimePublisherMessage(keys[0], req)
		if !ok {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid publish payload"))
			return
		}
		fields := map[string]json.RawMessage{}
		for name, value := range map[string]any{"delivery": api.ManagedRealtimeDeliveryRetained, "publisher": true, "event_type": req.EventType,
			"data_base64": req.DataBase64, "binary": req.Binary, "metadata": metadata, "ttl_seconds": req.TTLSeconds} {
			fields[name], _ = json.Marshal(value)
		}
		capture := state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindRealtimePublish, Endpoint: endpoint, Name: channel, IdempotencyKey: keys[0]}
		capture.Message, capture.Oversized = rehearsalPlatformMessage(fields, len(req.DataBase64))
		// apid answers an unknown endpoint as a refused credential.
		result, problem := captureRehearsalPlatformCall(r, call, capture, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Invalid publisher credential", "provide a current endpoint-bound publisher credential"),
			realtimePublishConflict())
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		if result.Replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		// Sequence 0 is never a history position: a captured publish is not
		// retained.
		writeRehearsalCaptured(w, http.StatusOK, map[string]any{"sequence": "0", "durable": true, "replayed": result.Replayed, "partial": false})
	}, true
}

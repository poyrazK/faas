// adr: 20261021180000
package gateway

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	rehearsalRealtimeBodyMaxBytes   int64 = 2 << 20 // base64 headroom over the 1 MiB message cap, as in apid
	rehearsalRealtimeSignalMaxBytes int64 = 4 << 10
	rehearsalRealtimeKeyMaxBytes          = 128
)

// realtimeRoute matches realtime/endpoints/{id}/channels/{channel}/{action}
// under an app.
func realtimeRoute(slug, rest string) (rehearsalPlatformRoute, bool) {
	parts := strings.Split(rest, "/")
	if len(parts) < 4 || parts[0] != "endpoints" || parts[1] == "" || parts[2] != "channels" || parts[3] == "" {
		return nil, false
	}
	endpoint, channel, action := parts[1], parts[3], parts[4:]
	switch {
	case len(action) == 1 && action[0] == "publish":
		return realtimePublishRoute(slug, endpoint, channel), true
	case len(action) == 1 && action[0] == "signals":
		return realtimeSignalRoute(slug, endpoint, channel, ""), true
	case len(action) == 3 && action[0] == "activity-scopes" && action[1] != "" && action[2] == "signals":
		return realtimeSignalRoute(slug, endpoint, channel, action[1]), true
	}
	return nil, false
}

func realtimeChannelProblem(channel string) *api.Problem {
	if !realtime.ValidateChannel(channel) {
		return api.ErrRealtimeInvalid("channel must be non-empty, at most 256 bytes, and contain no '/', '?', '#', or whitespace padding")
	}
	return nil
}

// realtimeDelivery mirrors apid's ?delivery= parsing.
func realtimeDelivery(r *http.Request) (api.ManagedRealtimeDelivery, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", false
	}
	values := query["delivery"]
	switch {
	case len(values) == 0:
		return api.ManagedRealtimeDeliveryLive, true
	case len(values) != 1:
		return "", false
	}
	delivery := api.ManagedRealtimeDelivery(values[0])
	return delivery, delivery == api.ManagedRealtimeDeliveryLive || delivery == api.ManagedRealtimeDeliveryRetained
}

// realtimePublishRoute captures a channel publish (ADR-20261021180000) with
// apid's message, delivery and Idempotency-Key rules.
func realtimePublishRoute(slug, endpoint, channel string) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		if problem := realtimeChannelProblem(channel); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) > 1 {
			api.WriteProblem(w, api.ErrRealtimeInvalid("send exactly one Idempotency-Key header"))
			return
		}
		key := ""
		if len(keys) == 1 {
			key = keys[0]
		}
		if key != "" && (len(key) > rehearsalRealtimeKeyMaxBytes || strings.IndexFunc(key, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0) {
			api.WriteProblem(w, api.ErrRealtimeInvalid("Idempotency-Key must contain 1 to 128 printable ASCII bytes with no whitespace"))
			return
		}
		delivery, ok := realtimeDelivery(r)
		if !ok {
			api.WriteProblem(w, api.ErrRealtimeInvalid("delivery must be live or retained, and may be specified once"))
			return
		}
		data, req, problem := decodeRehearsalRealtimeMessage(w, r, delivery, call.now)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		retained := delivery == api.ManagedRealtimeDeliveryRetained
		if retained && key == "" {
			api.WriteProblem(w, api.ErrRealtimeInvalid("delivery=retained requires exactly one Idempotency-Key header"))
			return
		}
		if retained && len(data) > state.ManagedRealtimeHistoryMaxPayloadBytes {
			api.WriteProblem(w, api.ErrRealtimeInvalid("retained messages are limited to 4096 decoded bytes"))
			return
		}
		fields := map[string]json.RawMessage{}
		for name, value := range map[string]any{"delivery": delivery, "data_base64": req.DataBase64, "binary": req.Binary, "metadata": req.Metadata,
			"ttl_seconds": req.TTLSeconds, "expected_sequence": req.ExpectedSequence} {
			fields[name], _ = json.Marshal(value)
		}
		capture := state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindRealtimePublish, Address: slug, Endpoint: endpoint, Name: channel, IdempotencyKey: key}
		capture.Message, capture.Oversized = rehearsalPlatformMessage(fields, len(req.DataBase64))
		result, problem := captureRehearsalPlatformCall(r, call, capture, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "managed realtime endpoint not found"),
			api.NewProblem(http.StatusConflict, api.CodeConflict, "Idempotency key conflict", "this Idempotency-Key was already used with a different realtime message"))
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		if result.Replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		// Nothing is delivered in a rehearsal: no subscriber is queued and no
		// history sequence is assigned.
		writeRehearsalCaptured(w, http.StatusOK, api.ManagedRealtimePublishResponse{NodesQueried: 1, Durable: retained})
	}
}

func decodeRehearsalRealtimeMessage(w http.ResponseWriter, r *http.Request, delivery api.ManagedRealtimeDelivery, now time.Time) ([]byte, api.ManagedRealtimeMessageRequest, *api.Problem) {
	var req api.ManagedRealtimeMessageRequest
	body, problem := readRehearsalPlatformBody(w, r, rehearsalRealtimeBodyMaxBytes, now)
	if problem != nil {
		return nil, req, api.ErrRealtimeInvalid(problem.Detail)
	}
	if problem := decodeRehearsalStrictJSON(body, &req); problem != nil {
		return nil, req, api.ErrRealtimeInvalid(problem.Detail)
	}
	data, err := base64.StdEncoding.DecodeString(req.DataBase64)
	if err != nil {
		return nil, req, api.ErrRealtimeInvalid("data_base64 must be valid standard base64")
	}
	if len(data) > api.RealtimeMessageMaxBytes {
		return nil, req, api.ErrRealtimeInvalid(fmt.Sprintf("message exceeds %d bytes", api.RealtimeMessageMaxBytes))
	}
	retained := delivery == api.ManagedRealtimeDeliveryRetained
	if req.ExpectedSequence != nil && (*req.ExpectedSequence < 0 || !retained) {
		return nil, req, api.ErrRealtimeInvalid("expected_sequence must be nonnegative and requires retained channel publishing")
	}
	if req.TTLSeconds != 0 && (req.TTLSeconds < 0 || req.TTLSeconds > api.RealtimeMessageTTLMaxSeconds || !retained) {
		return nil, req, api.ErrRealtimeInvalid("ttl_seconds requires retained channel publishing and must be between 1 and 86400")
	}
	if api.ValidateRealtimeMetadata(req.Metadata) != nil {
		return nil, req, api.ErrRealtimeInvalid("invalid message metadata")
	}
	return data, req, nil
}

// realtimeSignalRoute captures a backend signal, optionally into an activity
// scope, with apid's data, name and TTL rules.
func realtimeSignalRoute(slug, endpoint, channel, scope string) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		if scope != "" {
			scoped, err := api.RealtimeActivityScopeChannel(channel, scope)
			if err != nil {
				api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
				return
			}
			channel = scoped
		}
		if problem := realtimeChannelProblem(channel); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		invalid := api.ErrRealtimeInvalid("signal requires JSON data within 2048 bytes and the endpoint payload limit")
		body, problem := readRehearsalPlatformBody(w, r, rehearsalRealtimeSignalMaxBytes, call.now)
		var req api.ManagedRealtimeSignalRequest
		if problem != nil || decodeRehearsalStrictJSON(body, &req) != nil || len(req.Data) == 0 || len(req.Data) > realtime.ManagedRealtimeSignalDataMaxBytes || !json.Valid(req.Data) {
			api.WriteProblem(w, invalid)
			return
		}
		frame := realtime.EphemeralFrame{Type: "signal", MemberID: "backend", Data: req.Data, Name: req.Name, UpdatedAt: call.now}
		switch {
		case req.Name != "":
			ttl := 5000
			if req.TTLMS != nil {
				ttl = *req.TTLMS
			}
			if ttl < 0 || ttl > 30000 {
				api.WriteProblem(w, api.ErrRealtimeInvalid("ttl_ms must be from 0 to 30000"))
				return
			}
			expiry := call.now.Add(time.Duration(ttl) * time.Millisecond)
			frame.ExpiresAt = &expiry
		case req.TTLMS != nil:
			api.WriteProblem(w, api.ErrRealtimeInvalid("ttl_ms requires a signal name"))
			return
		}
		if !realtime.ValidateEphemeralFrame(frame) {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid signal name or data"))
			return
		}
		fields := map[string]json.RawMessage{"data": req.Data}
		if req.Name != "" {
			fields["name"], _ = json.Marshal(req.Name)
		}
		if req.TTLMS != nil {
			fields["ttl_ms"], _ = json.Marshal(*req.TTLMS)
		}
		capture := state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindRealtimeSignal, Address: slug, Endpoint: endpoint, Name: channel}
		capture.Message, capture.Oversized = rehearsalPlatformMessage(fields, len(req.Data))
		if _, problem := captureRehearsalPlatformCall(r, call, capture, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "managed realtime endpoint not found"), nil); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeRehearsalCaptured(w, http.StatusOK, api.ManagedRealtimeSignalResponse{Accepted: true, MemberID: "backend", ExpiresAt: frame.ExpiresAt})
	}
}

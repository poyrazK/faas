package outbound

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Gemini and Vertex Live sessions (ADR 20261020161811): the caller's first
// message, setup, names the model; each model turn is admitted when its
// output starts and settled from the turn's usageMetadata, which counts the
// context the turn read and the turn's own output.

// liveSetupTimeout bounds the wait for the caller's setup message.
const liveSetupTimeout = 10 * time.Second

// livePath reports the Live API's session paths. The ephemeral-token
// variant (BidiGenerateContentConstrained) is not one: its credential is
// the caller's.
func livePath(provider, path string) bool {
	switch provider {
	case api.OutboundAIProviderGemini:
		return path == "/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent" ||
			path == "/ws/google.ai.generativelanguage.v1alpha.GenerativeService.BidiGenerateContent"
	case api.OutboundAIProviderVertex:
		return path == "/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent" ||
			path == "/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent"
	}
	return false
}

// liveSetupModel is the model a setup message names, as the name after its
// last "models/": "models/gemini-2.5-flash-native-audio-preview-12-2025" or
// "projects/p/locations/l/publishers/google/models/gemini-live-2.5-flash".
func liveSetupModel(message []byte) string {
	var first struct {
		Setup *struct {
			Model string `json:"model"`
		} `json:"setup"`
	}
	if json.Unmarshal(message, &first) != nil || first.Setup == nil {
		return ""
	}
	name := first.Setup.Model
	if i := strings.LastIndex(name, "models/"); i >= 0 {
		name = name[i+len("models/"):]
	}
	return sanitizeAIModel(name)
}

// forwardLiveSetup reads the caller's first message for its model, then
// forwards its frames unchanged. A control frame before it passes at once.
// It reports false when the session ended instead.
func (s *realtimeSession) forwardLiveSetup(clientIn *bufio.Reader) bool {
	_ = s.client.SetReadDeadline(time.Now().Add(liveSetupTimeout))
	defer func() { _ = s.client.SetReadDeadline(time.Time{}) }()
	assembler := wsMessage{maxSize: api.OutboundAIRealtimeMaxMessageBytes, binary: true}
	var frames []byte
	for {
		frame, err := readWSFrame(clientIn, api.OutboundAIRealtimeMaxMessageBytes)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			s.finish(aiRealtimeClosedClient)
			return false
		}
		if err != nil {
			s.closeWith(aiRealtimeClosedUnreadable, "", "", "outbound_ai_realtime_unreadable")
			return false
		}
		if frame.opcode >= wsOpClose {
			if _, err := s.upstream.Write(append(frame.header, frame.payload...)); err != nil || frame.opcode == wsOpClose {
				s.finish(aiRealtimeClosedClient)
				return false
			}
			continue
		}
		frames = append(append(frames, frame.header...), frame.payload...)
		message, err := assembler.add(frame.unmasked())
		if err != nil {
			s.closeWith(aiRealtimeClosedUnreadable, "", "", "outbound_ai_realtime_unreadable")
			return false
		}
		if message == nil {
			continue
		}
		if s.model = liveSetupModel(message); s.model == "" {
			s.closeWith(aiRealtimeClosedUnreadable, "", "", "outbound_ai_realtime_unreadable")
			return false
		}
		if _, err := s.upstream.Write(frames); err != nil {
			s.finish(aiRealtimeClosedProvider)
			return false
		}
		return true
	}
}

// Markers of the Live messages metering reads; as with OpenAI's event
// types, a quoted key cannot appear unescaped inside a string, so a byte
// search skips the audio chunks of a turn already admitted.
var (
	liveOutputMarkers  = [][]byte{[]byte(`"modelTurn"`), []byte(`"outputTranscription"`), []byte(`"toolCall"`)}
	liveMeteredMarkers = [][]byte{[]byte(`"usageMetadata"`), []byte(`"turnComplete"`), []byte(`"interrupted"`),
		[]byte(`"groundingMetadata"`)}
)

// liveEvent is what metering reads from one provider message.
type liveEvent struct {
	ServerContent *struct {
		ModelTurn           json.RawMessage `json:"modelTurn"`
		OutputTranscription json.RawMessage `json:"outputTranscription"`
		TurnComplete        bool            `json:"turnComplete"`
		Interrupted         bool            `json:"interrupted"`
		GroundingMetadata   *struct {
			WebSearchQueries []string `json:"webSearchQueries"`
		} `json:"groundingMetadata"`
	} `json:"serverContent"`
	ToolCall      json.RawMessage `json:"toolCall"`
	UsageMetadata *aiUsageFields  `json:"usageMetadata"`
}

// output reports model output: a turn's content, its transcription or a
// function call.
func (e liveEvent) output() bool {
	if len(e.ToolCall) > 0 {
		return true
	}
	return e.ServerContent != nil && (len(e.ServerContent.ModelTurn) > 0 || len(e.ServerContent.OutputTranscription) > 0)
}

func containsAnyMarker(message []byte, markers [][]byte) bool {
	for _, marker := range markers {
		if bytes.Contains(message, marker) {
			return true
		}
	}
	return false
}

// meterLive reads one complete provider message of a Live session. It
// returns the end event when the message ends the session.
func (s *realtimeSession) meterLive(ctx context.Context, message []byte, now time.Time) string {
	output := containsAnyMarker(message, liveOutputMarkers)
	if !containsAnyMarker(message, liveMeteredMarkers) && (!output || s.generating) {
		return ""
	}
	var event liveEvent
	if err := json.Unmarshal(message, &event); err != nil {
		s.closeWith(aiRealtimeClosedUnreadable, "", "", "outbound_ai_realtime_unreadable")
		return aiRealtimeClosedUnreadable
	}
	if event.output() && !s.generating {
		if end := s.admitTurn(ctx, now); end != "" {
			return end
		}
	}
	if content := event.ServerContent; content != nil {
		if content.GroundingMetadata != nil && len(s.turns) > 0 {
			s.turns[len(s.turns)-1].grounding.noteGrounding([]aiGeminiCandidate{{GroundingMetadata: content.GroundingMetadata}})
		}
		if content.TurnComplete || content.Interrupted {
			s.generating = false
		}
	}
	if event.UsageMetadata != nil {
		s.settleTurn(ctx, event.UsageMetadata, now)
	}
	return ""
}

// admitTurn admits the model turn whose output just started.
func (s *realtimeSession) admitTurn(ctx context.Context, now time.Time) string {
	if len(s.turns) >= realtimeOpenResponsesMax {
		s.closeWith(aiRealtimeClosedUnreadable, "", "", "outbound_ai_realtime_unreadable")
		return aiRealtimeClosedUnreadable
	}
	turn, end := s.admit(ctx, now)
	if end != "" {
		return end
	}
	s.turns = append(s.turns, turn)
	s.generating = true
	return ""
}

// settleTurn settles the oldest open turn from a usageMetadata message:
// the context the turn read, its output and, as ADR-1433 bills them, its
// searches. Usage with no turn open settles without a lease.
func (s *realtimeSession) settleTurn(ctx context.Context, fields *aiUsageFields, now time.Time) {
	turn := &realtimeResponse{reserve: s.reservation(), started: now}
	if len(s.turns) > 0 {
		turn = s.turns[0]
		s.turns = s.turns[1:]
		if len(s.turns) == 0 {
			s.generating = false
		}
	}
	raw := turn.grounding
	raw.merge(fields)
	// Video frames price as image tokens (ADR 20261020161811).
	if video := geminiModalityTokens(fields.PromptTokensByModality, "VIDEO"); video != nil {
		raw.geminiPromptImage = min(raw.geminiPromptImage+*video, api.OutboundAIMaxTokensPerResponse)
	}
	usage := raw.normalize(aiFormatGemini, aiMediaNone)
	usage.Model = s.usageModel()
	usage.WebSearches = raw.webSearches(usage.Model)
	outcome := AIUsageOutcomeKnown
	if !usage.Known {
		outcome = AIUsageOutcomeUnknown
	} else {
		s.lastInput = usage.InputTokens
	}
	s.settle(ctx, turn, usage, outcome, aiMediaNone, now)
}

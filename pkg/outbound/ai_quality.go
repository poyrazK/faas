package outbound

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// Quality flags (ADR 20261021171712): marks a text generation's answer
// leaves when it went wrong, read from the fields providers return. The
// request says whether JSON was asked for and whether the app reported a
// failed tool; the response says whether the model stopped at its limit,
// refused, or answered nothing.

const (
	AIQualityTruncated   = api.OutboundAIQualityTruncated
	AIQualityRefused     = api.OutboundAIQualityRefused
	AIQualityEmpty       = api.OutboundAIQualityEmpty
	AIQualityInvalidJSON = api.OutboundAIQualityInvalidJSON
	AIQualityToolError   = api.OutboundAIQualityToolError
)

// aiQuality follows one response. checked is false for endpoints that
// generate no text (embeddings, media, batches), which get no flags.
type aiQuality struct {
	checked   bool
	jsonMode  bool
	toolError bool
	truncated bool
	refused   bool
	sawText   bool
	// malformedCall is a Gemini MALFORMED_FUNCTION_CALL finish: the model
	// produced a tool call the provider could not parse.
	malformedCall bool
	// text is the answer as far as OutboundAIQualityTextMaxBytes, kept only
	// in JSON mode for the invalid_json check.
	text         []byte
	textOverflow bool
}

// newAIQuality starts the checks of one call from its plan and buffered
// request.
func newAIQuality(plan *aiCallPlan, ai *aiRequest) aiQuality {
	if plan == nil || !plan.endpoint.generation || plan.endpoint.media != aiMediaNone || plan.endpoint.embedding || plan.endpoint.batch {
		return aiQuality{}
	}
	quality := aiQuality{checked: true}
	if ai != nil {
		quality.jsonMode = requestWantsJSON(ai.body)
		quality.toolError = requestReportsToolError(ai.body)
	}
	return quality
}

// Markers of the request fields the checks read; a byte search skips the
// parse for requests that carry none.
var (
	jsonModeMarkers  = [][]byte{[]byte(`"response_format"`), []byte(`"format"`), []byte(`esponseMimeType"`), []byte(`esponse_mime_type"`)}
	toolErrorMarkers = [][]byte{[]byte(`"is_error"`), []byte(`"toolResult"`)}
)

func containsAny(body []byte, markers [][]byte) bool {
	for _, marker := range markers {
		if bytes.Contains(body, marker) {
			return true
		}
	}
	return false
}

// requestWantsJSON reports an OpenAI response_format or Responses
// text.format of type json_object or json_schema, or a Gemini JSON
// response MIME type.
func requestWantsJSON(body []byte) bool {
	if !containsAny(body, jsonModeMarkers) {
		return false
	}
	type typed struct {
		Type string `json:"type"`
	}
	type mime struct {
		Camel string `json:"responseMimeType"`
		Snake string `json:"response_mime_type"`
	}
	var request struct {
		ResponseFormat *typed `json:"response_format"`
		Text           *struct {
			Format *typed `json:"format"`
		} `json:"text"`
		GenerationConfig      *mime `json:"generationConfig"`
		GenerationConfigSnake *mime `json:"generation_config"`
	}
	if json.Unmarshal(body, &request) != nil {
		return false
	}
	jsonType := func(t *typed) bool { return t != nil && (t.Type == "json_object" || t.Type == "json_schema") }
	jsonMIME := func(m *mime) bool {
		return m != nil && (m.Camel == "application/json" || m.Snake == "application/json")
	}
	return jsonType(request.ResponseFormat) || (request.Text != nil && jsonType(request.Text.Format)) ||
		jsonMIME(request.GenerationConfig) || jsonMIME(request.GenerationConfigSnake)
}

// requestReportsToolError reports a failed tool in the newest message: an
// Anthropic tool_result with is_error, or a Bedrock Converse toolResult
// with status error. Older messages repeat in every later request, so they
// would count the same failure again.
func requestReportsToolError(body []byte) bool {
	if !containsAny(body, toolErrorMarkers) {
		return false
	}
	var request struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &request) != nil || len(request.Messages) == 0 {
		return false
	}
	var blocks []struct {
		Type       string `json:"type"`
		IsError    bool   `json:"is_error"`
		ToolResult *struct {
			Status string `json:"status"`
		} `json:"toolResult"`
	}
	if json.Unmarshal(request.Messages[len(request.Messages)-1].Content, &blocks) != nil {
		return false
	}
	for _, block := range blocks {
		if (block.Type == "tool_result" && block.IsError) || (block.ToolResult != nil && block.ToolResult.Status == "error") {
			return true
		}
	}
	return false
}

// addText notes answer text, keeping it in JSON mode.
func (q *aiQuality) addText(text string) {
	if text == "" {
		return
	}
	q.sawText = true
	if !q.jsonMode || q.textOverflow {
		return
	}
	if len(q.text)+len(text) > api.OutboundAIQualityTextMaxBytes {
		q.text, q.textOverflow = nil, true
		return
	}
	q.text = append(q.text, text...)
}

// stop notes a provider's stop or finish reason.
func (q *aiQuality) stop(reason string) {
	switch reason {
	case "length", "max_tokens", "max_output_tokens", "MAX_TOKENS", "model_context_window_exceeded":
		q.truncated = true
	case "content_filter", "refusal", "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII",
		"guardrail_intervened", "content_filtered":
		q.refused = true
	case "MALFORMED_FUNCTION_CALL":
		q.malformedCall = true
	}
}

// flags are the call's quality flags, given the tools its response
// requested and whether any of their arguments failed to parse.
func (q *aiQuality) flags(tools []string, invalidArguments bool) []string {
	if !q.checked {
		return nil
	}
	var flags []string
	if q.truncated {
		flags = append(flags, AIQualityTruncated)
	}
	if q.refused {
		flags = append(flags, AIQualityRefused)
	}
	if !q.sawText && len(tools) == 0 && !q.truncated && !q.refused {
		flags = append(flags, AIQualityEmpty)
	}
	if invalidArguments || q.malformedCall || (q.jsonMode && q.sawText && !q.textOverflow && !json.Valid(bytes.TrimSpace(q.text))) {
		flags = append(flags, AIQualityInvalidJSON)
	}
	if q.toolError {
		flags = append(flags, AIQualityToolError)
	}
	return flags
}

// noteQuality reads one envelope's stop reasons, refusals and answer text,
// in every format the gateway meters.
func (o *aiUsageObserver) noteQuality(envelope aiEnvelope) {
	q := &o.quality
	if !q.checked {
		return
	}
	if len(envelope.Choices) > 0 {
		noteChatQuality(q, envelope.Choices)
	}
	for _, reason := range []json.RawMessage{envelope.StopReason, envelope.BedrockStopReason} {
		if text, ok := rawString(reason); ok {
			q.stop(text)
		}
	}
	noteResponsesStatus(q, envelope.Status, envelope.IncompleteDetails)
	if envelope.Response != nil {
		noteResponsesStatus(q, envelope.Response.Status, envelope.Response.IncompleteDetails)
		noteOutputQuality(q, envelope.Response.Output)
	}
	noteOutputQuality(q, envelope.Output)
	noteAnthropicText(q, envelope.Content)
	noteDeltaQuality(q, envelope.Type, envelope.Delta)
	if len(envelope.Candidates) > 0 {
		noteGeminiQuality(q, envelope.Candidates)
	}
	if bytes.Contains(envelope.PromptFeedback, []byte(`"blockReason"`)) {
		q.refused = true
	}
}

// rawString reads a JSON string. Envelope fields stay raw, so an unexpected
// value never fails the parse that finds usage.
func rawString(raw json.RawMessage) (string, bool) {
	var text string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &text) != nil {
		return "", false
	}
	return text, true
}

// noteChatQuality reads OpenAI Chat Completions and legacy completions
// choices: finish reasons, refusals and content, whole or streamed.
func noteChatQuality(q *aiQuality, raw json.RawMessage) {
	type holder struct {
		Content json.RawMessage `json:"content"`
		Refusal *string         `json:"refusal"`
	}
	var choices []struct {
		FinishReason *string `json:"finish_reason"`
		Text         string  `json:"text"`
		Message      *holder `json:"message"`
		Delta        *holder `json:"delta"`
	}
	if json.Unmarshal(raw, &choices) != nil {
		return
	}
	for _, choice := range choices {
		if choice.FinishReason != nil {
			q.stop(*choice.FinishReason)
		}
		q.addText(choice.Text)
		for _, part := range []*holder{choice.Message, choice.Delta} {
			if part == nil {
				continue
			}
			if part.Refusal != nil && *part.Refusal != "" {
				q.refused = true
			}
			q.addText(contentText(part.Content))
		}
	}
}

// contentText reads a chat message's content: a string, or an array of
// parts with text.
func contentText(raw json.RawMessage) string {
	if text, ok := rawString(raw); ok {
		return text
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var text string
	for _, part := range parts {
		text += part.Text
	}
	return text
}

// noteResponsesStatus reads an OpenAI Responses object's incomplete
// reason.
func noteResponsesStatus(q *aiQuality, status, details json.RawMessage) {
	if text, _ := rawString(status); text != "incomplete" {
		return
	}
	var incomplete struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(details, &incomplete) == nil {
		q.stop(incomplete.Reason)
	}
}

// noteOutputQuality reads an OpenAI Responses output array's message text
// and refusals, or a Bedrock Converse output message's text. A Responses
// stream sends the text again in its final response, which is the copy
// kept for the JSON check.
func noteOutputQuality(q *aiQuality, raw json.RawMessage) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return
	}
	if trimmed[0] == '[' {
		var items []struct {
			Type    string `json:"type"`
			Content []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		}
		if json.Unmarshal(trimmed, &items) != nil {
			return
		}
		for _, item := range items {
			if item.Type != "message" {
				continue
			}
			for _, part := range item.Content {
				switch part.Type {
				case "output_text":
					q.addText(part.Text)
				case "refusal":
					q.refused = true
				}
			}
		}
		return
	}
	var output struct {
		Message struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(trimmed, &output) == nil {
		for _, block := range output.Message.Content {
			q.addText(block.Text)
		}
	}
}

// noteAnthropicText reads a whole Anthropic message's text blocks.
func noteAnthropicText(q *aiQuality, raw json.RawMessage) {
	if len(raw) == 0 || raw[0] != '[' {
		return
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return
	}
	for _, block := range blocks {
		if block.Type == "text" {
			q.addText(block.Text)
		}
	}
}

// noteDeltaQuality reads a stream delta: a Responses text or refusal delta
// (a string), an Anthropic text delta or message delta, or a Bedrock
// ConverseStream text delta. A Responses delta only marks that text came;
// its final response carries the text.
func noteDeltaQuality(q *aiQuality, eventType string, raw json.RawMessage) {
	if text, ok := rawString(raw); ok {
		switch eventType {
		case "response.output_text.delta":
			if text != "" {
				q.sawText = true
			}
		case "response.refusal.delta":
			q.refused = true
		}
		return
	}
	if len(raw) == 0 || raw[0] != '{' {
		return
	}
	var delta struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
	}
	if json.Unmarshal(raw, &delta) != nil {
		return
	}
	if delta.Type == "" || delta.Type == "text_delta" {
		q.addText(delta.Text)
	}
	q.stop(delta.StopReason)
}

// noteGeminiQuality reads Gemini candidates: finish reasons and the text
// of parts that are not thoughts.
func noteGeminiQuality(q *aiQuality, raw json.RawMessage) {
	var candidates []struct {
		FinishReason string `json:"finishReason"`
		Content      struct {
			Parts []struct {
				Text    string `json:"text"`
				Thought bool   `json:"thought"`
			} `json:"parts"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &candidates) != nil {
		return
	}
	for _, candidate := range candidates {
		q.stop(candidate.FinishReason)
		for _, part := range candidate.Content.Parts {
			if !part.Thought {
				q.addText(part.Text)
			}
		}
	}
}

// validateAIQualityRecord checks a settlement's call ID and quality flags.
func validateAIQualityRecord(record AIUsageRecord) error {
	if record.CallID != "" && !validUUID(record.CallID) {
		return fmt.Errorf("%w: AI usage call id must be a UUID", ErrInvalidIntegration)
	}
	known := []string{AIQualityTruncated, AIQualityRefused, AIQualityEmpty, AIQualityInvalidJSON, AIQualityToolError}
	for index, flag := range record.QualityFlags {
		if !slices.Contains(known, flag) || slices.Contains(record.QualityFlags[:index], flag) {
			return fmt.Errorf("%w: AI usage quality flag %q is invalid", ErrInvalidIntegration, flag)
		}
	}
	return nil
}

// sampleForOnlineEvaluation decides whether an answered, captured app call
// is judged (ADR 20261021171712). Settlement still checks that both bodies
// were captured whole and that the day has evaluations left.
func sampleForOnlineEvaluation(integration Integration, record AIUsageRecord) bool {
	percent := integration.AIOnlineEvalPercent
	if percent <= 0 || record.Outcome != AIUsageOutcomeKnown || !appCaller(record.Caller) || record.Log == nil ||
		record.Log.Content == nil || record.Log.Status < 200 || record.Log.Status > 299 {
		return false
	}
	return percent >= 100 || rand.IntN(100) < percent //nolint:gosec // sampling needs no cryptographic randomness.
}

// qualityFlags are the finished response's quality flags.
func (o *aiUsageObserver) qualityFlags() []string {
	return o.quality.flags(o.tools.names(), o.tools.invalidArguments())
}

package outbound

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AIUsage is the normalized token usage of one provider response (ADR-979).
// InputTokens includes cached reads and cache writes, which are also
// reported separately, so every provider counts input the same way.
type AIUsage struct {
	Model             string
	Known             bool
	InputTokens       int64
	CachedInputTokens int64
	CacheWriteTokens  int64
	OutputTokens      int64
}

// BudgetTokens is the amount a response consumes from a daily token limit.
func (u AIUsage) BudgetTokens() int64 { return u.InputTokens + u.OutputTokens }

// AIUsageOutcome classifies a recorded AI call. Errors carry no tokens.
type AIUsageOutcome string

const (
	AIUsageOutcomeKnown   AIUsageOutcome = "known"
	AIUsageOutcomeUnknown AIUsageOutcome = "unknown"
	AIUsageOutcomeError   AIUsageOutcome = "error"
)

// AIUsageRecord is one settled AI call. Caller is the verified app ID, or
// AIUsageCallerRuns for a stateless Run.
type AIUsageRecord struct {
	IntegrationID string
	Caller        string
	Outcome       AIUsageOutcome
	Usage         AIUsage
}

// AIUsageCallerRuns aggregates every Run's calls; Runs have no app ID.
const AIUsageCallerRuns = "runs"

// aiUsageOverflowModel collects models beyond the per-day distinct cap.
const aiUsageOverflowModel = "other"

// validateAIUsageRecord checks backend-neutral bounds. The Postgres backend
// additionally requires UUID integration and caller IDs (or "runs").
func validateAIUsageRecord(record AIUsageRecord) error {
	if strings.TrimSpace(record.IntegrationID) == "" || strings.TrimSpace(record.Caller) == "" || len(record.Caller) > 64 {
		return fmt.Errorf("%w: AI usage record needs an integration and caller", ErrInvalidIntegration)
	}
	switch record.Outcome {
	case AIUsageOutcomeKnown, AIUsageOutcomeUnknown, AIUsageOutcomeError:
	default:
		return fmt.Errorf("%w: AI usage outcome is invalid", ErrInvalidIntegration)
	}
	usage := record.Usage
	if usage.Model == "" || len(usage.Model) > api.OutboundAIModelNameMaxBytes || sanitizeAIModel(usage.Model) != usage.Model {
		return fmt.Errorf("%w: AI usage model is invalid", ErrInvalidIntegration)
	}
	for _, value := range []int64{usage.InputTokens, usage.CachedInputTokens, usage.CacheWriteTokens, usage.OutputTokens} {
		if value < 0 || value > 3*api.OutboundAIMaxTokensPerResponse {
			return fmt.Errorf("%w: AI usage token count is out of range", ErrInvalidIntegration)
		}
	}
	return nil
}

// aiUsageFields covers the OpenAI chat completions, OpenAI Responses and
// Anthropic Messages usage objects. Absent fields stay nil.
type aiUsageFields struct {
	PromptTokens        *int64 `json:"prompt_tokens"`
	CompletionTokens    *int64 `json:"completion_tokens"`
	InputTokens         *int64 `json:"input_tokens"`
	OutputTokens        *int64 `json:"output_tokens"`
	PromptTokensDetails *struct {
		CachedTokens *int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	InputTokensDetails *struct {
		CachedTokens *int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
}

type aiEnvelopeInner struct {
	Model string         `json:"model"`
	Usage *aiUsageFields `json:"usage"`
}

// aiEnvelope finds usage at the top level (chat completions, Anthropic
// responses and message_delta), under "message" (Anthropic message_start) or
// under "response" (OpenAI Responses stream events).
type aiEnvelope struct {
	Model    string           `json:"model"`
	Usage    *aiUsageFields   `json:"usage"`
	Message  *aiEnvelopeInner `json:"message"`
	Response *aiEnvelopeInner `json:"response"`
}

// aiRawUsage keeps the largest value seen per provider field. Streaming
// providers repeat or grow counts across events, never shrink them.
type aiRawUsage struct {
	prompt, completion, input, output, cached, cacheRead, cacheCreation int64
	seenPrompt, seenCompletion, seen                                    bool
}

func (r *aiRawUsage) merge(fields *aiUsageFields) {
	if fields == nil {
		return
	}
	take := func(dst *int64, src *int64) bool {
		if src == nil {
			return false
		}
		value := *src
		if value < 0 {
			value = 0
		}
		if value > api.OutboundAIMaxTokensPerResponse {
			value = api.OutboundAIMaxTokensPerResponse
		}
		if value > *dst {
			*dst = value
		}
		r.seen = true
		return true
	}
	if take(&r.prompt, fields.PromptTokens) {
		r.seenPrompt = true
	}
	if take(&r.completion, fields.CompletionTokens) {
		r.seenCompletion = true
	}
	take(&r.input, fields.InputTokens)
	take(&r.output, fields.OutputTokens)
	if fields.PromptTokensDetails != nil {
		take(&r.cached, fields.PromptTokensDetails.CachedTokens)
	}
	if fields.InputTokensDetails != nil {
		take(&r.cached, fields.InputTokensDetails.CachedTokens)
	}
	take(&r.cacheRead, fields.CacheReadInputTokens)
	take(&r.cacheCreation, fields.CacheCreationInputTokens)
}

func (r aiRawUsage) normalize(provider string) AIUsage {
	usage := AIUsage{Known: r.seen}
	switch provider {
	case api.OutboundAIProviderAnthropic:
		usage.InputTokens = r.input + r.cacheRead + r.cacheCreation
		usage.CachedInputTokens = r.cacheRead
		usage.CacheWriteTokens = r.cacheCreation
		usage.OutputTokens = r.output
	default:
		usage.InputTokens = r.input
		if r.seenPrompt {
			usage.InputTokens = r.prompt
		}
		usage.OutputTokens = r.output
		if r.seenCompletion {
			usage.OutputTokens = r.completion
		}
		usage.CachedInputTokens = min(r.cached, usage.InputTokens)
	}
	return usage
}

type aiBodyMode int

const (
	aiBodyIgnored aiBodyMode = iota
	aiBodyJSON
	aiBodyEventStream
)

// aiUsageObserver receives a copy of the response bytes the gateway sends to
// the caller. It never fails a write, so it cannot slow or break the stream.
type aiUsageObserver struct {
	provider     string
	mode         aiBodyMode
	now          func() time.Time
	firstByteAt  time.Time
	body         []byte
	bodyOverflow bool
	line         []byte
	lineOverflow bool
	data         []byte
	dataOverflow bool
	raw          aiRawUsage
	model        string
}

func newAIUsageObserver(provider, contentType string, now func() time.Time) *aiUsageObserver {
	if now == nil {
		now = time.Now
	}
	o := &aiUsageObserver{provider: provider, now: now}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(contentType))
	}
	switch {
	case mediaType == "text/event-stream":
		o.mode = aiBodyEventStream
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		o.mode = aiBodyJSON
	}
	return o
}

func (o *aiUsageObserver) Write(p []byte) (int, error) {
	if len(p) > 0 && o.firstByteAt.IsZero() {
		o.firstByteAt = o.now()
	}
	switch o.mode {
	case aiBodyJSON:
		o.writeJSON(p)
	case aiBodyEventStream:
		o.writeEventStream(p)
	}
	return len(p), nil
}

func (o *aiUsageObserver) writeJSON(p []byte) {
	if o.bodyOverflow {
		return
	}
	if len(o.body)+len(p) > api.OutboundAIUsageJSONParseMaxBytes {
		o.body, o.bodyOverflow = nil, true
		return
	}
	o.body = append(o.body, p...)
}

func (o *aiUsageObserver) writeEventStream(p []byte) {
	for len(p) > 0 {
		newline := bytes.IndexByte(p, '\n')
		chunk := p
		if newline >= 0 {
			chunk = p[:newline]
		}
		if !o.lineOverflow {
			if len(o.line)+len(chunk) > api.OutboundAIUsageEventMaxBytes {
				o.line, o.lineOverflow = o.line[:0], true
			} else {
				o.line = append(o.line, chunk...)
			}
		}
		if newline < 0 {
			return
		}
		o.endLine()
		p = p[newline+1:]
	}
}

// endLine applies one complete server-sent-event line. A blank line ends the
// event; only data fields matter for usage.
func (o *aiUsageObserver) endLine() {
	line := bytes.TrimSuffix(o.line, []byte{'\r'})
	overflow := o.lineOverflow
	o.line, o.lineOverflow = o.line[:0], false
	if overflow {
		o.dataOverflow = true
		return
	}
	if len(line) == 0 {
		o.dispatchEvent()
		return
	}
	value, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok || o.dataOverflow {
		return
	}
	value = bytes.TrimPrefix(value, []byte{' '})
	if len(o.data)+len(value)+1 > api.OutboundAIUsageEventMaxBytes {
		o.data, o.dataOverflow = o.data[:0], true
		return
	}
	if len(o.data) > 0 {
		o.data = append(o.data, '\n')
	}
	o.data = append(o.data, value...)
}

func (o *aiUsageObserver) dispatchEvent() {
	data, overflow := o.data, o.dataOverflow
	o.data, o.dataOverflow = o.data[:0], false
	if overflow || len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return
	}
	o.applyJSON(data)
}

func (o *aiUsageObserver) applyJSON(data []byte) {
	var envelope aiEnvelope
	if json.Unmarshal(data, &envelope) != nil {
		return
	}
	o.raw.merge(envelope.Usage)
	o.noteModel(envelope.Model)
	for _, inner := range []*aiEnvelopeInner{envelope.Message, envelope.Response} {
		if inner != nil {
			o.raw.merge(inner.Usage)
			o.noteModel(inner.Model)
		}
	}
}

func (o *aiUsageObserver) noteModel(model string) {
	if o.model == "" {
		o.model = sanitizeAIModel(model)
	}
}

// Finish parses any buffered JSON body or trailing event and returns the
// normalized usage. Call it once, after the response copy ends.
func (o *aiUsageObserver) Finish() AIUsage {
	switch o.mode {
	case aiBodyJSON:
		if !o.bodyOverflow && len(o.body) > 0 {
			o.applyJSON(o.body)
		}
		o.body = nil
	case aiBodyEventStream:
		if len(o.line) > 0 || o.lineOverflow {
			o.endLine()
		}
		o.dispatchEvent()
	}
	usage := o.raw.normalize(o.provider)
	usage.Model = o.model
	if usage.Model == "" {
		usage.Model = "unknown"
	}
	return usage
}

// FirstByteAt reports when the first response body byte was observed.
func (o *aiUsageObserver) FirstByteAt() time.Time { return o.firstByteAt }

// sanitizeAIModel bounds a provider-reported model name to printable ASCII so
// it is safe to store and display. An empty result means unknown.
func sanitizeAIModel(model string) string {
	var b strings.Builder
	for i := 0; i < len(model) && b.Len() < api.OutboundAIModelNameMaxBytes; i++ {
		if c := model[i]; c >= 0x20 && c <= 0x7e {
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String())
}

// aiUsageOutcome classifies a settled call from its status and usage.
func aiUsageOutcome(status int, usage AIUsage) AIUsageOutcome {
	if status < 200 || status > 299 {
		return AIUsageOutcomeError
	}
	if !usage.Known {
		return AIUsageOutcomeUnknown
	}
	return AIUsageOutcomeKnown
}

// streamingMediaType reports whether a response is an incremental stream the
// gateway must flush as it arrives instead of buffering.
func streamingMediaType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == "text/event-stream" || mediaType == "application/x-ndjson"
}

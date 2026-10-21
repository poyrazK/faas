package outbound

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AIUsage is the normalized token usage of one provider response (ADR-988).
// InputTokens includes cached reads and cache writes, which are also
// reported separately, so every provider counts input the same way.
type AIUsage struct {
	Model             string
	Known             bool
	InputTokens       int64
	CachedInputTokens int64
	CacheWriteTokens  int64
	OutputTokens      int64
	// Media usage (ADR-997). InputImageTokens and OutputImageTokens are the
	// image parts of InputTokens and OutputTokens. Images counts images a
	// call is billed for per image, AudioSeconds the input audio of a
	// transcription, rounded up, and Characters the input text of speech.
	InputImageTokens  int64
	OutputImageTokens int64
	Images            int64
	AudioSeconds      int64
	Characters        int64
	// VideoSeconds is seconds of generated video, and VideoResolution the
	// resolution they are priced at; it is not stored (ADR-1432).
	VideoSeconds    int64
	VideoResolution string
	// Built-in tool uses (ADR-1433): web searches as the provider bills
	// them (calls, queries or grounded prompts), file searches, and GB of
	// code-interpreter container sessions.
	WebSearches   int64
	FileSearches  int64
	CodeSessionGB int64
	// Audio usage (ADR 20261019072722): InputAudioTokens and
	// OutputAudioTokens are the audio parts of InputTokens and OutputTokens,
	// and CachedInputAudioTokens the audio part of CachedInputTokens.
	InputAudioTokens       int64
	CachedInputAudioTokens int64
	OutputAudioTokens      int64
}

// priceModel is the name a call is priced under: a video call's model
// with its resolution, so "<model>@<resolution>" prices win over the
// model's own (ADR-1432).
func (u AIUsage) priceModel() string {
	if u.VideoSeconds > 0 && u.VideoResolution != "" {
		return u.Model + "@" + u.VideoResolution
	}
	return u.Model
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
	// CostMillicents is priced at settlement from the integration's price
	// list (ADR-990); zero when no price matches.
	CostMillicents int64
	// Log, when set, adds one metadata row to the AI request log (ADR-991).
	Log *AIRequestLog
	// EndUser is the call's X-Gregale-AI-User value, or empty (ADR-994).
	EndUser string
	// Tags are the call's usage tags, sorted by key (ADR-1436).
	Tags []string
	// LeaseID is the call's admission lease. It keys the settlement, so a
	// retried settlement counts the call once, and the settlement deletes
	// the lease (ADR-1120). Empty settles without a key.
	LeaseID string
	// Route is a route attempt while the route runs a canary; it adds to
	// the target's per-minute row (ADR-1340).
	Route *AIRouteAttemptRecord
	// Export is the call's line for the calling app's log drains
	// (ADR-1340).
	Export *AILogExport
	// Cached marks a response the cache served; it has no tokens or cost,
	// and CacheSavedMillicents is what the provider call would have cost.
	// PromptCacheSavedMillicents is what provider prompt caching saved on a
	// provider call, negative when cache writes outweigh reads (ADR-1440).
	Cached                     bool
	CacheSavedMillicents       int64
	PromptCacheSavedMillicents int64
}

// AIRouteAttemptRecord is one settled attempt of a route with a canary.
// Error marks a provider status of 400 or above; TTFBMS is set for a
// successful attempt that received a first byte.
type AIRouteAttemptRecord struct {
	RouteID  string
	Position int
	Error    bool
	TTFBMS   *int64
}

// AILogExport is one export line and its OTLP attributes (ADR-1340).
type AILogExport struct {
	AppID      string
	AccountID  string
	Line       string
	Attributes map[string]string
}

// AIRequestLog is the non-usage metadata of one logged AI call. It holds
// content only when the integration opted in to capture (ADR-996).
type AIRequestLog struct {
	RouteID    string
	Status     int
	DurationMS int64
	// TTFBMS is nil when no response byte arrived.
	TTFBMS   *int64
	Attempts int
	// Content is the call's captured bodies, or nil.
	Content *AIRequestContent
}

// AIRequestContent is the start of one logged call's bodies as the gateway
// sent them to the provider and read them back: at most
// api.OutboundAIRequestContentMaxBytes each, never headers (ADR-996). A
// request body the gateway streamed without buffering is empty and
// truncated.
type AIRequestContent struct {
	// RequestPath is the escaped provider path of the call (ADR-997).
	RequestPath         string
	Request             []byte
	RequestTruncated    bool
	Response            []byte
	ResponseTruncated   bool
	ResponseContentType string
}

// aiContentCapture keeps the first bytes of a response as the gateway
// forwards it, plus a little more so a secret cut at the limit is still
// redacted (ADR-997).
type aiContentCapture struct {
	content AIRequestContent
	seen    int
}

func newAIContentCapture(ai *aiRequest, contentType string) *aiContentCapture {
	capture := &aiContentCapture{content: AIRequestContent{RequestTruncated: true, ResponseContentType: contentType}}
	if ai != nil {
		prefix := ai.body[:min(len(ai.body), api.OutboundAIRequestContentMaxBytes+api.OutboundAICaptureRedactSlackBytes)]
		capture.content.Request, capture.content.RequestTruncated = redactCaptured(prefix, len(ai.body) > api.OutboundAIRequestContentMaxBytes)
	}
	if len(contentType) > aiContentTypeMaxBytes || strings.IndexFunc(contentType, func(r rune) bool { return r < ' ' || r > '~' }) >= 0 {
		capture.content.ResponseContentType = ""
	}
	return capture
}

func (c *aiContentCapture) Write(p []byte) {
	c.seen += len(p)
	room := api.OutboundAIRequestContentMaxBytes + api.OutboundAICaptureRedactSlackBytes - len(c.content.Response)
	if room <= 0 {
		return
	}
	c.content.Response = append(c.content.Response, p[:min(len(p), room)]...)
}

// finish redacts the captured response and bounds it to the capture limit.
func (c *aiContentCapture) finish() *AIRequestContent {
	c.content.Response, c.content.ResponseTruncated = redactCaptured(c.content.Response, c.seen > api.OutboundAIRequestContentMaxBytes)
	return &c.content
}

const aiContentTypeMaxBytes = 128

func validateAIRequestContent(content *AIRequestContent) bool {
	return content == nil || (len(content.Request) <= api.OutboundAIRequestContentMaxBytes && len(content.RequestPath) <= 2048 &&
		len(content.Response) <= api.OutboundAIRequestContentMaxBytes && len(content.ResponseContentType) <= aiContentTypeMaxBytes)
}

func newAIRequestLog(settlement aiSettlement, timeToFirstByte time.Duration, now time.Time) *AIRequestLog {
	log := &AIRequestLog{
		RouteID: settlement.routeID, Status: settlement.status, Attempts: settlement.attempts,
		DurationMS: max(now.Sub(settlement.started).Milliseconds(), 0),
	}
	if timeToFirstByte > 0 {
		ttfb := timeToFirstByte.Milliseconds()
		log.TTFBMS = &ttfb
	}
	return log
}

func validateAIRequestLog(log *AIRequestLog) error {
	if log == nil {
		return nil
	}
	if log.Status < 100 || log.Status > 599 || log.DurationMS < 0 || log.DurationMS > 1<<31-1 ||
		(log.TTFBMS != nil && (*log.TTFBMS < 0 || *log.TTFBMS > 1<<31-1)) || log.Attempts < 0 || log.Attempts > 100 ||
		(log.RouteID != "" && !validUUID(log.RouteID)) || !validateAIRequestContent(log.Content) {
		return fmt.Errorf("%w: AI request log entry is out of range", ErrInvalidIntegration)
	}
	return nil
}

// AIUsageCallerRuns aggregates every Run's calls; Runs have no app ID.
const AIUsageCallerRuns = "runs"

// AIUsageCallerReplay aggregates the calls of replays (ADR-997); they have
// no app ID either.
const AIUsageCallerReplay = "replay"

// AIUsageCallerModeration aggregates moderation checks (ADR-1340).
const AIUsageCallerModeration = "moderation"

// AIUsageCallerEvaluation aggregates evaluations' candidate and judge calls
// (ADR-1431).
const AIUsageCallerEvaluation = "evaluation"

// appCaller reports whether a settled caller is an app ID, which has app
// budgets, minute counters and alert webhooks.
func appCaller(caller string) bool {
	switch caller {
	case AIUsageCallerRuns, AIUsageCallerReplay, AIUsageCallerModeration, AIUsageCallerEvaluation, aiBatchReader:
		return false
	}
	return true
}

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
	for _, value := range []int64{usage.InputTokens, usage.CachedInputTokens, usage.CacheWriteTokens, usage.OutputTokens,
		usage.InputImageTokens, usage.OutputImageTokens, usage.Images, usage.AudioSeconds, usage.Characters, usage.VideoSeconds,
		usage.WebSearches, usage.FileSearches, usage.CodeSessionGB} {
		if value < 0 || value > 3*api.OutboundAIMaxTokensPerResponse {
			return fmt.Errorf("%w: AI usage token count is out of range", ErrInvalidIntegration)
		}
	}
	if record.CostMillicents < 0 || record.CostMillicents > api.MaxOutboundAICostMillicents {
		return fmt.Errorf("%w: AI usage cost is out of range", ErrInvalidIntegration)
	}
	if record.CacheSavedMillicents < 0 || record.CacheSavedMillicents > api.MaxOutboundAICostMillicents ||
		record.PromptCacheSavedMillicents < -api.MaxOutboundAICostMillicents || record.PromptCacheSavedMillicents > api.MaxOutboundAICostMillicents ||
		(!record.Cached && record.CacheSavedMillicents != 0) ||
		(record.Cached && (usage.BudgetTokens() != 0 || record.CostMillicents != 0 || record.PromptCacheSavedMillicents != 0)) {
		return fmt.Errorf("%w: AI usage cache savings are invalid", ErrInvalidIntegration)
	}
	if !validAITags(record.Tags) {
		return fmt.Errorf("%w: AI usage tags are invalid", ErrInvalidIntegration)
	}
	if record.EndUser != "" && !api.ValidOutboundAIUserID(record.EndUser) {
		return fmt.Errorf("%w: AI usage end user is invalid", ErrInvalidIntegration)
	}
	if record.LeaseID != "" && !validUUID(record.LeaseID) {
		return fmt.Errorf("%w: AI usage lease id must be a UUID", ErrInvalidIntegration)
	}
	if err := validateAIRouteAttempt(record.Route); err != nil {
		return err
	}
	if err := validateAILogExport(record.Export); err != nil {
		return err
	}
	return validateAIRequestLog(record.Log)
}

func validateAIRouteAttempt(attempt *AIRouteAttemptRecord) error {
	if attempt == nil {
		return nil
	}
	if !validUUID(attempt.RouteID) || attempt.Position < 1 || attempt.Position > api.OutboundAIRouteTargetsMax ||
		(attempt.TTFBMS != nil && (*attempt.TTFBMS < 0 || *attempt.TTFBMS > 1<<31-1)) {
		return fmt.Errorf("%w: AI route attempt is out of range", ErrInvalidIntegration)
	}
	return nil
}

func validateAILogExport(export *AILogExport) error {
	if export == nil {
		return nil
	}
	if !validUUID(export.AppID) || !validUUID(export.AccountID) || len(export.Line) < 2 ||
		len(export.Line) > api.OutboundAILogExportLineMaxBytes || len(export.Attributes) > aiLogExportAttributesMax {
		return fmt.Errorf("%w: AI log export line is out of range", ErrInvalidIntegration)
	}
	return nil
}

// aiUsageFields covers the OpenAI chat completions, OpenAI Responses,
// Anthropic Messages, Gemini usageMetadata, Bedrock Converse usage and Bedrock
// invocation metrics objects (ADR-991). Absent fields stay nil.
type aiUsageFields struct {
	PromptTokens        *int64 `json:"prompt_tokens"`
	CompletionTokens    *int64 `json:"completion_tokens"`
	InputTokens         *int64 `json:"input_tokens"`
	OutputTokens        *int64 `json:"output_tokens"`
	PromptTokensDetails *struct {
		CachedTokens *int64 `json:"cached_tokens"`
		AudioTokens  *int64 `json:"audio_tokens"`
	} `json:"prompt_tokens_details"`
	// Chat completions audio output (ADR 20261019072722).
	CompletionTokensDetails *struct {
		AudioTokens *int64 `json:"audio_tokens"`
	} `json:"completion_tokens_details"`
	InputTokensDetails *openAITokenDetails `json:"input_tokens_details"`
	// OpenAI image generation output and transcription duration usage
	// (ADR-997).
	OutputTokensDetails *openAITokenDetails `json:"output_tokens_details"`
	// OpenAI Realtime usage names its details in the singular
	// (ADR 20261019072722).
	InputTokenDetails  *openAITokenDetails `json:"input_token_details"`
	OutputTokenDetails *openAITokenDetails `json:"output_token_details"`
	Seconds            *float64            `json:"seconds"`

	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	// Anthropic server tool uses (ADR-1433).
	ServerToolUse *struct {
		WebSearchRequests *int64 `json:"web_search_requests"`
	} `json:"server_tool_use"`
	// Gemini usageMetadata.
	PromptTokenCount        *int64 `json:"promptTokenCount"`
	CandidatesTokenCount    *int64 `json:"candidatesTokenCount"`
	CachedContentTokenCount *int64 `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      *int64 `json:"thoughtsTokenCount"`
	// Gemini token counts by modality (ADR-997).
	PromptTokensByModality     []geminiModalityCount `json:"promptTokensDetails"`
	CandidatesTokensByModality []geminiModalityCount `json:"candidatesTokensDetails"`
	// The Gemini API's Live usage names output "response" tokens
	// (ADR 20261020161811).
	ResponseTokenCount       *int64                `json:"responseTokenCount"`
	ResponseTokensByModality []geminiModalityCount `json:"responseTokensDetails"`
	// Bedrock Converse usage.
	BedrockInputTokens      *int64 `json:"inputTokens"`
	BedrockOutputTokens     *int64 `json:"outputTokens"`
	BedrockCacheReadTokens  *int64 `json:"cacheReadInputTokens"`
	BedrockCacheWriteTokens *int64 `json:"cacheWriteInputTokens"`
	// Bedrock invocation metrics.
	InputTokenCount           *int64 `json:"inputTokenCount"`
	OutputTokenCount          *int64 `json:"outputTokenCount"`
	CacheReadInputTokenCount  *int64 `json:"cacheReadInputTokenCount"`
	CacheWriteInputTokenCount *int64 `json:"cacheWriteInputTokenCount"`
}

// openAITokenDetails is a token breakdown by kind: cached, image and audio
// tokens, and the cached tokens' own breakdown.
type openAITokenDetails struct {
	CachedTokens        *int64 `json:"cached_tokens"`
	ImageTokens         *int64 `json:"image_tokens"`
	AudioTokens         *int64 `json:"audio_tokens"`
	CachedTokensDetails *struct {
		AudioTokens *int64 `json:"audio_tokens"`
	} `json:"cached_tokens_details"`
}

type geminiModalityCount struct {
	Modality   string `json:"modality"`
	TokenCount *int64 `json:"tokenCount"`
}

// geminiModalityTokens sums one modality's counts (IMAGE, AUDIO), or
// returns nil.
func geminiModalityTokens(counts []geminiModalityCount, modality string) *int64 {
	var total int64
	seen := false
	for _, count := range counts {
		if count.Modality == modality && count.TokenCount != nil {
			total += max(*count.TokenCount, 0)
			seen = true
		}
	}
	if !seen {
		return nil
	}
	return &total
}

type aiEnvelopeInner struct {
	Model string         `json:"model"`
	Usage *aiUsageFields `json:"usage"`
	// Output is an OpenAI Responses object's output items (ADR-1433).
	Output json.RawMessage `json:"output"`
}

// aiToolItem is one OpenAI Responses output item, as far as built-in tool
// fees need it (ADR-1433).
type aiToolItem struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	ContainerID string `json:"container_id"`
}

// aiGeminiCandidate carries a Gemini candidate's search grounding
// (ADR-1433).
type aiGeminiCandidate struct {
	GroundingMetadata *struct {
		WebSearchQueries []string `json:"webSearchQueries"`
	} `json:"groundingMetadata"`
}

// aiEnvelope finds usage at the top level (chat completions, Anthropic
// responses and message_delta, Bedrock Converse), under "message" (Anthropic
// message_start), under "response" (OpenAI Responses stream events), in
// Gemini "usageMetadata" and in Bedrock invocation metrics. Bytes carries a
// base64 provider event inside a Bedrock invoke stream chunk.
type aiEnvelope struct {
	Model         string           `json:"model"`
	ModelVersion  string           `json:"modelVersion"`
	Usage         *aiUsageFields   `json:"usage"`
	UsageMetadata *aiUsageFields   `json:"usageMetadata"`
	Metrics       *aiUsageFields   `json:"amazon-bedrock-invocationMetrics"`
	Message       *aiEnvelopeInner `json:"message"`
	Response      *aiEnvelopeInner `json:"response"`
	Bytes         []byte           `json:"bytes"`
	// Type names an OpenAI stream event; Duration is a verbose
	// transcription's audio length (ADR-997).
	Type     string   `json:"type"`
	Duration *float64 `json:"duration"`
	// Output is a Responses object's output items, or Bedrock Converse's
	// output message, which tool counting skips; Item is one Responses
	// stream event's item; Candidates carry Gemini search grounding
	// (ADR-1433).
	Output     json.RawMessage `json:"output"`
	Item       json.RawMessage `json:"item"`
	Candidates json.RawMessage `json:"candidates"`
	// Predictions carries Vertex AI text-embedding token counts (ADR-995).
	Predictions []struct {
		Embeddings *struct {
			Statistics *struct {
				TokenCount *int64 `json:"token_count"`
			} `json:"statistics"`
		} `json:"embeddings"`
	} `json:"predictions"`
}

// vertexPredictionTokens sums the token counts of Vertex AI embedding
// predictions, or returns nil when none reports one.
func vertexPredictionTokens(envelope aiEnvelope) *int64 {
	var total int64
	seen := false
	for _, prediction := range envelope.Predictions {
		if prediction.Embeddings != nil && prediction.Embeddings.Statistics != nil && prediction.Embeddings.Statistics.TokenCount != nil {
			total += max(*prediction.Embeddings.Statistics.TokenCount, 0)
			seen = true
		}
	}
	if !seen {
		return nil
	}
	return &total
}

// aiRawUsage keeps the largest value seen per provider field. Streaming
// providers repeat or grow counts across events, never shrink them.
type aiRawUsage struct {
	prompt, completion, input, output, cached, cacheRead, cacheCreation int64
	seenPrompt, seenCompletion, seen                                    bool
	// Gemini, Bedrock Converse and Bedrock invocation metrics counts.
	geminiPrompt, geminiCandidates, geminiCached, geminiThoughts     int64
	bedrockInput, bedrockOutput, bedrockCacheRead, bedrockCacheWrite int64
	metricInput, metricOutput, metricCacheRead, metricCacheWrite     int64
	seenMetrics                                                      bool
	// Media counts (ADR-997).
	inputImage, outputImage, geminiPromptImage, geminiCandidatesImage int64
	seenOutputImage                                                   bool
	images, audioSeconds                                              int64
	// Audio token counts (ADR 20261019072722).
	inputAudio, cachedInputAudio, outputAudio, geminiPromptAudio, geminiCandidatesAudio int64
	// Built-in tool uses (ADR-1433). webSearchRequests is Anthropic's;
	// the Responses counts take the larger of a final response's items and
	// the items streamed one by one.
	webSearchRequests                  int64
	responsesWebSearch, responsesFiles int64
	streamedTools                      map[string]string
	containers                         []string
	geminiQueries                      int64
	geminiGrounded                     bool
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
		take(&r.inputAudio, fields.PromptTokensDetails.AudioTokens)
	}
	if fields.CompletionTokensDetails != nil {
		take(&r.outputAudio, fields.CompletionTokensDetails.AudioTokens)
	}
	for _, details := range []*openAITokenDetails{fields.InputTokensDetails, fields.InputTokenDetails} {
		if details == nil {
			continue
		}
		take(&r.cached, details.CachedTokens)
		take(&r.inputImage, details.ImageTokens)
		take(&r.inputAudio, details.AudioTokens)
		if details.CachedTokensDetails != nil {
			take(&r.cachedInputAudio, details.CachedTokensDetails.AudioTokens)
		}
	}
	for _, details := range []*openAITokenDetails{fields.OutputTokensDetails, fields.OutputTokenDetails} {
		if details == nil {
			continue
		}
		if take(&r.outputImage, details.ImageTokens) {
			r.seenOutputImage = true
		}
		take(&r.outputAudio, details.AudioTokens)
	}
	take(&r.cacheRead, fields.CacheReadInputTokens)
	take(&r.cacheCreation, fields.CacheCreationInputTokens)
	if fields.ServerToolUse != nil && fields.ServerToolUse.WebSearchRequests != nil {
		r.webSearchRequests = max(r.webSearchRequests, min(max(*fields.ServerToolUse.WebSearchRequests, 0), api.OutboundAIToolUsesPerCallMax))
	}
	take(&r.geminiPrompt, fields.PromptTokenCount)
	take(&r.geminiCandidates, fields.CandidatesTokenCount)
	take(&r.geminiCandidates, fields.ResponseTokenCount)
	take(&r.geminiCached, fields.CachedContentTokenCount)
	take(&r.geminiThoughts, fields.ThoughtsTokenCount)
	take(&r.bedrockInput, fields.BedrockInputTokens)
	take(&r.bedrockOutput, fields.BedrockOutputTokens)
	take(&r.bedrockCacheRead, fields.BedrockCacheReadTokens)
	take(&r.bedrockCacheWrite, fields.BedrockCacheWriteTokens)
	take(&r.geminiPromptImage, geminiModalityTokens(fields.PromptTokensByModality, "IMAGE"))
	take(&r.geminiCandidatesImage, geminiModalityTokens(fields.CandidatesTokensByModality, "IMAGE"))
	take(&r.geminiPromptAudio, geminiModalityTokens(fields.PromptTokensByModality, "AUDIO"))
	take(&r.geminiCandidatesAudio, geminiModalityTokens(fields.CandidatesTokensByModality, "AUDIO"))
	take(&r.geminiCandidatesImage, geminiModalityTokens(fields.ResponseTokensByModality, "IMAGE"))
	take(&r.geminiCandidatesAudio, geminiModalityTokens(fields.ResponseTokensByModality, "AUDIO"))
	if fields.Seconds != nil {
		r.noteAudioSeconds(*fields.Seconds)
	}
	for _, metric := range []bool{
		take(&r.metricInput, fields.InputTokenCount), take(&r.metricOutput, fields.OutputTokenCount),
		take(&r.metricCacheRead, fields.CacheReadInputTokenCount), take(&r.metricCacheWrite, fields.CacheWriteInputTokenCount),
	} {
		r.seenMetrics = r.seenMetrics || metric
	}
}

// noteToolItems counts a final Responses object's web and file searches
// and keeps its containers (ADR-1433).
func (r *aiRawUsage) noteToolItems(raw json.RawMessage) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return
	}
	var items []aiToolItem
	if json.Unmarshal(trimmed, &items) != nil {
		return
	}
	var web, files int64
	for _, item := range items {
		switch item.Type {
		case "web_search_call":
			web++
		case "file_search_call":
			files++
		case "code_interpreter_call":
			r.noteContainer(item.ContainerID)
		}
	}
	r.responsesWebSearch = max(r.responsesWebSearch, min(web, api.OutboundAIToolUsesPerCallMax))
	r.responsesFiles = max(r.responsesFiles, min(files, api.OutboundAIToolUsesPerCallMax))
}

// noteStreamedItem counts one Responses stream item once by its ID.
func (r *aiRawUsage) noteStreamedItem(item *aiToolItem) {
	if item == nil || item.ID == "" || len(r.streamedTools) >= api.OutboundAIToolUsesPerCallMax {
		return
	}
	switch item.Type {
	case "web_search_call", "file_search_call":
		if r.streamedTools == nil {
			r.streamedTools = make(map[string]string)
		}
		r.streamedTools[item.ID] = item.Type
	case "code_interpreter_call":
		r.noteContainer(item.ContainerID)
	}
}

// noteContainer keeps a code-interpreter container a response used.
func (r *aiRawUsage) noteContainer(id string) {
	if id == "" || len(id) > 128 || len(r.containers) >= aiContainersPerResponseMax {
		return
	}
	for _, seen := range r.containers {
		if seen == id {
			return
		}
	}
	r.containers = append(r.containers, id)
}

// aiContainersPerResponseMax bounds the containers one response names.
const aiContainersPerResponseMax = 16

// noteGrounding keeps the most non-empty search queries a Gemini chunk
// reports (ADR-1433).
func (r *aiRawUsage) noteGrounding(candidates []aiGeminiCandidate) {
	for _, candidate := range candidates {
		if candidate.GroundingMetadata == nil {
			continue
		}
		var queries int64
		for _, query := range candidate.GroundingMetadata.WebSearchQueries {
			if strings.TrimSpace(query) != "" {
				queries++
			}
		}
		if queries > 0 {
			r.geminiGrounded = true
			r.geminiQueries = max(r.geminiQueries, min(queries, api.OutboundAIToolUsesPerCallMax))
		}
	}
}

// webSearches is the web searches a response reports, as its provider
// bills them: Gemini 3 and newer per query, Gemini 2.5 and older per
// grounded prompt (ADR-1433).
func (r aiRawUsage) webSearches(model string) int64 {
	streamedWeb := int64(0)
	for _, kind := range r.streamedTools {
		if kind == "web_search_call" {
			streamedWeb++
		}
	}
	searches := r.webSearchRequests + max(r.responsesWebSearch, streamedWeb)
	if r.geminiGrounded {
		if geminiBillsPerPrompt(model) {
			searches++
		} else {
			searches += r.geminiQueries
		}
	}
	return min(searches, api.OutboundAIToolUsesPerCallMax)
}

func (r aiRawUsage) fileSearches() int64 {
	streamed := int64(0)
	for _, kind := range r.streamedTools {
		if kind == "file_search_call" {
			streamed++
		}
	}
	return max(r.responsesFiles, streamed)
}

// geminiBillsPerPrompt reports a Gemini model whose search grounding is
// billed per grounded prompt: Gemini 2.5 and older (ADR-1433).
func geminiBillsPerPrompt(model string) bool {
	model = strings.TrimPrefix(model, "models/")
	return strings.HasPrefix(model, "gemini-1") || strings.HasPrefix(model, "gemini-2")
}

// noteAudioSeconds keeps the longest reported audio duration, rounded up.
func (r *aiRawUsage) noteAudioSeconds(seconds float64) {
	if math.IsNaN(seconds) || seconds <= 0 {
		return
	}
	rounded := int64(math.Ceil(min(seconds, float64(api.OutboundAIMaxTokensPerResponse))))
	r.audioSeconds = max(r.audioSeconds, rounded)
	r.seen = true
}

// noteImages counts images billed per image.
func (r *aiRawUsage) noteImages(count int64) {
	if count > 0 {
		r.images = min(r.images+count, api.OutboundAIMaxTokensPerResponse)
		r.seen = true
	}
}

func (r aiRawUsage) normalize(format aiWireFormat, media aiMediaKind) AIUsage {
	usage := r.normalizeTokens(format, media)
	usage.Images = r.images
	if media == aiMediaTranscription {
		usage.AudioSeconds = r.audioSeconds
	}
	return usage
}

func (r aiRawUsage) normalizeTokens(format aiWireFormat, media aiMediaKind) AIUsage {
	usage := AIUsage{Known: r.seen}
	switch {
	case format == aiFormatBedrockInvoke && r.seenMetrics:
		usage.InputTokens = r.metricInput + r.metricCacheRead + r.metricCacheWrite
		usage.CachedInputTokens = r.metricCacheRead
		usage.CacheWriteTokens = r.metricCacheWrite
		usage.OutputTokens = r.metricOutput
	case format == aiFormatAnthropic || format == aiFormatBedrockInvoke:
		usage.InputTokens = r.input + r.cacheRead + r.cacheCreation
		usage.CachedInputTokens = r.cacheRead
		usage.CacheWriteTokens = r.cacheCreation
		usage.OutputTokens = r.output
	case format == aiFormatGemini:
		// promptTokenCount includes cached content; thinking is billed as
		// output.
		usage.InputTokens = r.geminiPrompt
		usage.CachedInputTokens = min(r.geminiCached, r.geminiPrompt)
		usage.OutputTokens = r.geminiCandidates + r.geminiThoughts
		usage.InputImageTokens = min(r.geminiPromptImage, usage.InputTokens)
		usage.OutputImageTokens = min(r.geminiCandidatesImage, usage.OutputTokens)
		usage.InputAudioTokens = min(r.geminiPromptAudio, usage.InputTokens-usage.InputImageTokens)
		usage.OutputAudioTokens = min(r.geminiCandidatesAudio, usage.OutputTokens-usage.OutputImageTokens)
	case format == aiFormatBedrockConverse:
		usage.InputTokens = r.bedrockInput + r.bedrockCacheRead + r.bedrockCacheWrite
		usage.CachedInputTokens = r.bedrockCacheRead
		usage.CacheWriteTokens = r.bedrockCacheWrite
		usage.OutputTokens = r.bedrockOutput
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
		usage.InputImageTokens = min(r.inputImage, usage.InputTokens)
		// Image generation output is image tokens unless the response
		// splits it (ADR-997).
		usage.OutputImageTokens = min(r.outputImage, usage.OutputTokens)
		if media == aiMediaImage && !r.seenOutputImage {
			usage.OutputImageTokens = usage.OutputTokens
		}
		// ADR 20261019072722: audio is the rest of a part, never more.
		usage.InputAudioTokens = min(r.inputAudio, usage.InputTokens-usage.InputImageTokens)
		usage.CachedInputAudioTokens = min(r.cachedInputAudio, usage.CachedInputTokens, usage.InputAudioTokens)
		usage.OutputAudioTokens = min(r.outputAudio, usage.OutputTokens-usage.OutputImageTokens)
	}
	return usage
}

type aiBodyMode int

const (
	aiBodyIgnored aiBodyMode = iota
	aiBodyJSON
	aiBodyEventStream
	// aiBodyAWSEventStream is Bedrock's binary
	// application/vnd.amazon.eventstream framing.
	aiBodyAWSEventStream
)

// aiUsageObserver receives a copy of the response bytes the gateway sends to
// the caller. It never fails a write, so it cannot slow or break the stream.
type aiUsageObserver struct {
	format aiWireFormat
	mode   aiBodyMode
	frames awsEventStreamReader
	// fallbackModel names the call when the response does not (Bedrock
	// Converse, Vertex paths).
	fallbackModel string
	now           func() time.Time
	firstByteAt   time.Time
	body          []byte
	bodyOverflow  bool
	line          []byte
	lineOverflow  bool
	data          []byte
	dataOverflow  bool
	raw           aiRawUsage
	model         string
	// capture keeps the call's bodies when the integration opted in
	// (ADR-996); observedBody feeds it.
	capture *aiContentCapture
	// media is the endpoint's media kind. A JSON response larger than the
	// parse limit is read from its tail for usage, and its images are
	// counted by marker (ADR-997).
	media        aiMediaKind
	tail         []byte
	imageMarkers markerCounter
	parsedImages bool
	// chatSearch marks a chat completion on a search model, which runs one
	// web search per call (ADR-1433).
	chatSearch bool
}

// newAIUsageObserver observes a call in the provider's default format.
func newAIUsageObserver(provider, contentType string, now func() time.Time) *aiUsageObserver {
	format := aiFormatOpenAI
	switch provider {
	case api.OutboundAIProviderAnthropic:
		format = aiFormatAnthropic
	case api.OutboundAIProviderGemini:
		format = aiFormatGemini
	}
	return newAIUsageObserverFormat(format, contentType, now)
}

// newAIUsageObserverFormat observes one call of a known wire format.
func newAIUsageObserverFormat(format aiWireFormat, contentType string, now func() time.Time) *aiUsageObserver {
	if now == nil {
		now = time.Now
	}
	o := &aiUsageObserver{format: format, now: now}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(contentType))
	}
	switch {
	case mediaType == "text/event-stream":
		o.mode = aiBodyEventStream
	case mediaType == awsEventStreamMediaType:
		o.mode = aiBodyAWSEventStream
		o.frames.onPayload = o.applyAWSEventPayload
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		o.mode = aiBodyJSON
	}
	return o
}

// applyHeaders reads Bedrock invoke token-count response headers.
func (o *aiUsageObserver) applyHeaders(header http.Header) {
	input, inputOK := headerTokenCount(header, "X-Amzn-Bedrock-Input-Token-Count")
	output, outputOK := headerTokenCount(header, "X-Amzn-Bedrock-Output-Token-Count")
	if !inputOK && !outputOK {
		return
	}
	fields := &aiUsageFields{}
	if inputOK {
		fields.InputTokenCount = &input
	}
	if outputOK {
		fields.OutputTokenCount = &output
	}
	o.raw.merge(fields)
}

func headerTokenCount(header http.Header, name string) (int64, bool) {
	value := strings.TrimSpace(header.Get(name))
	if value == "" {
		return 0, false
	}
	count, err := strconv.ParseInt(value, 10, 64)
	return count, err == nil && count >= 0
}

// applyAWSEventPayload handles one Bedrock event-stream frame payload. A
// converse-stream metadata event carries usage directly; an invoke stream
// chunk wraps a base64 provider event in "bytes".
func (o *aiUsageObserver) applyAWSEventPayload(payload []byte) {
	var envelope aiEnvelope
	if json.Unmarshal(payload, &envelope) != nil {
		return
	}
	if len(envelope.Bytes) > 0 {
		o.applyJSON(envelope.Bytes)
		return
	}
	o.applyEnvelope(envelope)
}

func (o *aiUsageObserver) Write(p []byte) (int, error) {
	if len(p) > 0 && o.firstByteAt.IsZero() {
		o.firstByteAt = o.now()
	}
	if o.media == aiMediaImage && o.mode == aiBodyJSON {
		o.imageMarkers.write(p)
	}
	switch o.mode {
	case aiBodyJSON:
		o.writeJSON(p)
	case aiBodyEventStream:
		o.writeEventStream(p)
	case aiBodyAWSEventStream:
		o.frames.write(p)
	}
	return len(p), nil
}

func (o *aiUsageObserver) writeJSON(p []byte) {
	if o.bodyOverflow {
		o.keepTail(p)
		return
	}
	if len(o.body)+len(p) > api.OutboundAIUsageJSONParseMaxBytes {
		// Keep only the end, where providers put usage (ADR-997).
		o.keepTail(o.body)
		o.keepTail(p)
		o.body, o.bodyOverflow = nil, true
		return
	}
	o.body = append(o.body, p...)
}

func (o *aiUsageObserver) keepTail(p []byte) {
	if len(p) >= api.OutboundAIUsageTailBytes {
		o.tail = append(o.tail[:0], p[len(p)-api.OutboundAIUsageTailBytes:]...)
		return
	}
	if excess := len(o.tail) + len(p) - api.OutboundAIUsageTailBytes; excess > 0 {
		o.tail = append(o.tail[:0], o.tail[excess:]...)
	}
	o.tail = append(o.tail, p...)
}

// applyTail reads the last usage or usageMetadata object of an oversized
// JSON response from its tail.
func (o *aiUsageObserver) applyTail() {
	for _, key := range []string{`"usage"`, `"usageMetadata"`} {
		index := bytes.LastIndex(o.tail, []byte(key))
		if index < 0 {
			continue
		}
		rest := bytes.TrimLeft(o.tail[index+len(key):], " \t\r\n")
		rest, ok := bytes.CutPrefix(rest, []byte{':'})
		if !ok {
			continue
		}
		var fields aiUsageFields
		if json.NewDecoder(bytes.NewReader(rest)).Decode(&fields) == nil {
			o.raw.merge(&fields)
		}
	}
	o.tail = nil
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
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		// Gemini streamGenerateContent without alt=sse returns one array.
		var envelopes []aiEnvelope
		if json.Unmarshal(trimmed, &envelopes) == nil {
			for _, envelope := range envelopes {
				o.applyEnvelope(envelope)
			}
		}
		return
	}
	var envelope aiEnvelope
	if json.Unmarshal(trimmed, &envelope) != nil {
		return
	}
	o.applyEnvelope(envelope)
	if o.media == aiMediaImage && o.mode == aiBodyJSON {
		o.applyImageCount(trimmed)
	}
}

// applyImageCount counts the images of an OpenAI images response ("data")
// or an Imagen predict response (predictions with image bytes).
func (o *aiUsageObserver) applyImageCount(body []byte) {
	var images struct {
		Data        []json.RawMessage `json:"data"`
		Predictions []struct {
			Image json.RawMessage `json:"bytesBase64Encoded"`
		} `json:"predictions"`
	}
	if json.Unmarshal(body, &images) != nil {
		return
	}
	count := int64(len(images.Data))
	for _, prediction := range images.Predictions {
		if len(prediction.Image) > 0 {
			count++
		}
	}
	o.raw.noteImages(count)
	o.parsedImages = true
}

func (o *aiUsageObserver) applyEnvelope(envelope aiEnvelope) {
	o.raw.merge(envelope.Usage)
	o.raw.merge(envelope.UsageMetadata)
	o.raw.merge(envelope.Metrics)
	switch {
	case o.media == aiMediaImage && (envelope.Type == "image_generation.completed" || envelope.Type == "image_edit.completed"):
		o.raw.noteImages(1)
	case o.media == aiMediaTranscription && envelope.Duration != nil:
		o.raw.noteAudioSeconds(*envelope.Duration)
	}
	if tokens := vertexPredictionTokens(envelope); tokens != nil {
		o.raw.merge(&aiUsageFields{PromptTokenCount: tokens})
	}
	// ADR-1433: built-in tool uses.
	o.raw.noteToolItems(envelope.Output)
	if envelope.Type == "response.output_item.done" && len(envelope.Item) > 0 {
		var item aiToolItem
		if json.Unmarshal(envelope.Item, &item) == nil {
			o.raw.noteStreamedItem(&item)
		}
	}
	if len(envelope.Candidates) > 0 {
		var candidates []aiGeminiCandidate
		if json.Unmarshal(envelope.Candidates, &candidates) == nil {
			o.raw.noteGrounding(candidates)
		}
	}
	o.noteModel(envelope.Model)
	o.noteModel(envelope.ModelVersion)
	for _, inner := range []*aiEnvelopeInner{envelope.Message, envelope.Response} {
		if inner != nil {
			o.raw.merge(inner.Usage)
			o.noteModel(inner.Model)
			o.raw.noteToolItems(inner.Output)
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
		if o.bodyOverflow {
			o.applyTail()
		}
		if o.media == aiMediaImage && !o.parsedImages {
			o.raw.noteImages(o.imageMarkers.count)
		}
		o.body = nil
	case aiBodyEventStream:
		if len(o.line) > 0 || o.lineOverflow {
			o.endLine()
		}
		o.dispatchEvent()
	}
	usage := o.raw.normalize(o.format, o.media)
	usage.Model = o.model
	if usage.Model == "" {
		usage.Model = sanitizeAIModel(o.fallbackModel)
	}
	if usage.Model == "" {
		usage.Model = "unknown"
	}
	// ADR-1433: built-in tool uses; a chat search model runs one search
	// per call.
	usage.WebSearches, usage.FileSearches = o.raw.webSearches(usage.Model), o.raw.fileSearches()
	if o.chatSearch && usage.Known {
		usage.WebSearches = max(usage.WebSearches, 1)
	}
	return usage
}

// containerIDs lists the code-interpreter containers a response used
// (ADR-1433).
func (o *aiUsageObserver) containerIDs() []string { return o.raw.containers }

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
	return mediaType == "text/event-stream" || mediaType == "application/x-ndjson" || mediaType == awsEventStreamMediaType
}

// markerCounter counts the image markers of an OpenAI images ("b64_json")
// or Imagen ("bytesBase64Encoded") response across write boundaries.
// Base64 data cannot contain a quote, so a marker is never found inside an
// image (ADR-997).
type markerCounter struct {
	carry []byte
	count int64
}

var imageMarkers = [][]byte{[]byte(`"b64_json"`), []byte(`"bytesBase64Encoded"`)}

const imageMarkerMaxBytes = len(`"bytesBase64Encoded"`)

func (m *markerCounter) write(p []byte) {
	carried := len(m.carry)
	window := append(m.carry, p...)
	for _, marker := range imageMarkers {
		for offset := 0; ; {
			index := bytes.Index(window[offset:], marker)
			if index < 0 {
				break
			}
			// A marker that ends inside the carried bytes was counted by
			// the previous write.
			if offset+index+len(marker) > carried {
				m.count++
			}
			offset += index + 1
		}
	}
	keep := min(len(window), imageMarkerMaxBytes-1)
	m.carry = append(m.carry[:0:0], window[len(window)-keep:]...)
}

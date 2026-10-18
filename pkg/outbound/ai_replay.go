package outbound

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/airedact"
)

// Replay and compare (ADR-997): apid records a replay of one captured call
// against up to five targets; outboundd's worker claims it and sends the
// captured request to each target through the ordinary integration path, so
// budgets, guardrails, the content policy and accounting all apply. The
// calls settle under the "replay" caller.

// replayPrincipal marks an in-process replay call. Only the replay worker
// sets it; no header or relay connection can.
type replayPrincipal struct {
	accountID, replayID string
	outcome             *replayOutcome
}

type replayPrincipalKey struct{}

func withReplayPrincipal(ctx context.Context, principal replayPrincipal) context.Context {
	return context.WithValue(ctx, replayPrincipalKey{}, principal)
}

func replayPrincipalFromContext(ctx context.Context) (replayPrincipal, bool) {
	principal, ok := ctx.Value(replayPrincipalKey{}).(replayPrincipal)
	return principal, ok && principal.accountID != ""
}

// replayAllowed limits a replay to the account's own customer AI
// integrations.
func replayAllowed(integration Integration, principal replayPrincipal) bool {
	return integration.AccountID == principal.accountID && integration.OwnerKind == IntegrationOwnerCustomer &&
		integration.ProviderAuthMode == ProviderAuthManaged && integration.AIProvider != ""
}

// replayOutcome is what settlement reports back to the worker.
type replayOutcome struct {
	settled bool
	outcome AIUsageOutcome
	usage   AIUsage
	cost    int64
}

// AIReplayJob is one claimed replay with its captured call.
type AIReplayJob struct {
	ID            string
	AccountID     string
	IntegrationID string
	Targets       []AIRouteTarget
	// The captured call: its model as logged, escaped provider path,
	// request body and response.
	SourceModel         string
	RequestPath         string
	RequestBody         []byte
	ResponseBody        []byte
	ResponseContentType string
}

// AIReplayResult is one target's replayed call. Status is 0 when the
// gateway sent no response, and Outcome is empty when the call did not
// settle (it was refused before reaching the provider).
type AIReplayResult struct {
	IntegrationID       string
	Model               string
	Status              int
	Outcome             AIUsageOutcome
	ErrorCode           string
	Usage               AIUsage
	CostMillicents      int64
	DurationMS          int64
	TTFBMS              *int64
	ResponseBody        []byte
	ResponseTruncated   bool
	ResponseContentType string
	OutputText          string
}

// AIReplayStore hands replays to the worker and keeps their results.
type AIReplayStore interface {
	ClaimAIReplay(ctx context.Context) (AIReplayJob, bool, error)
	FinishAIReplay(ctx context.Context, job AIReplayJob, sourceOutputText string, results []AIReplayResult, errorCode string) error
}

// AIReplayWorker runs claimed replays through Handler.
type AIReplayWorker struct {
	Store    AIReplayStore
	Handler  *Handler
	Interval time.Duration
	Log      *slog.Logger
}

// Run processes replays until ctx ends, polling when none is pending.
func (w *AIReplayWorker) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for {
		processed, err := w.ProcessOne(ctx)
		if err != nil && ctx.Err() == nil && w.Log != nil {
			w.Log.Warn("outboundd: AI replay", "err", err)
		}
		if processed && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// ProcessOne runs at most one replay and reports whether it claimed one.
func (w *AIReplayWorker) ProcessOne(ctx context.Context) (bool, error) {
	job, ok, err := w.Store.ClaimAIReplay(ctx)
	if err != nil || !ok {
		return false, err
	}
	sourceText := extractOutputText(job.ResponseBody, job.ResponseContentType)
	source, err := w.Handler.Resolver.Integration(ctx, job.IntegrationID)
	if err != nil {
		w.Handler.Metrics.ObserveAIReplay(api.OutboundAIReplayFailed)
		return true, w.Store.FinishAIReplay(ctx, job, sourceText, nil, "source_unavailable")
	}
	results := make([]AIReplayResult, 0, len(job.Targets))
	for _, target := range job.Targets {
		results = append(results, w.replayTarget(ctx, job, source, target))
	}
	w.Handler.Metrics.ObserveAIReplay(api.OutboundAIReplayDone)
	return true, w.Store.FinishAIReplay(ctx, job, sourceText, results, "")
}

func (w *AIReplayWorker) replayTarget(ctx context.Context, job AIReplayJob, source Integration, target AIRouteTarget) AIReplayResult {
	result := AIReplayResult{IntegrationID: target.IntegrationID, Model: target.Model}
	integration, err := w.Handler.Resolver.Integration(ctx, target.IntegrationID)
	if err != nil || !replayAllowed(integration, replayPrincipal{accountID: job.AccountID}) {
		result.ErrorCode = "outbound_replay_target_unavailable"
		return result
	}
	attempt, err := buildReplayAttempt(job, source, integration, target.Model)
	result.Model = attempt.model
	if err != nil {
		result.ErrorCode = "outbound_replay_unsupported"
		return result
	}
	outcome := &replayOutcome{}
	callCtx, cancel := context.WithTimeout(withReplayPrincipal(ctx, replayPrincipal{accountID: job.AccountID, replayID: job.ID, outcome: outcome}),
		api.OutboundAIReplayCallTimeoutSeconds*time.Second)
	defer cancel()
	request := routeAttemptRequest(&http.Request{Method: http.MethodPost, Header: http.Header{"Content-Type": {"application/json"}}}, target.IntegrationID,
		attempt.path, attempt.rawQuery, attempt.body, true).WithContext(callCtx)
	recorder := &replayRecorder{header: make(http.Header), now: time.Now}
	started := time.Now()
	serveReplay(w.Handler, recorder, request, attempt.translator)
	result.DurationMS = time.Since(started).Milliseconds()
	if !recorder.firstByte.IsZero() {
		ttfb := recorder.firstByte.Sub(started).Milliseconds()
		result.TTFBMS = &ttfb
	}
	result.Status = recorder.status
	result.ResponseContentType = recorder.header.Get("Content-Type")
	if outcome.settled {
		result.Outcome, result.Usage, result.CostMillicents = outcome.outcome, outcome.usage, outcome.cost
	}
	if recorder.status >= http.StatusBadRequest {
		result.ErrorCode = problemCode(recorder.body, result.ResponseContentType)
	}
	result.OutputText = redactedOutputText(recorder.body, result.ResponseContentType)
	result.ResponseBody, result.ResponseTruncated = redactCaptured(recorder.body, recorder.seen > api.OutboundAIRequestContentMaxBytes)
	if len(result.ResponseContentType) > aiContentTypeMaxBytes {
		result.ResponseContentType = ""
	}
	return result
}

// serveReplay runs one call in process. The handler aborts a broken
// response stream with http.ErrAbortHandler, which ends this call only.
func serveReplay(handler *Handler, recorder *replayRecorder, request *http.Request, translator aiResponseTranslator) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recovered != http.ErrAbortHandler { //nolint:errorlint // the handler panics with this exact sentinel value.
				panic(recovered)
			}
			recorder.aborted = true
		}
	}()
	if translator != nil {
		translating := newTranslatingWriter(recorder, translator)
		handler.ServeHTTP(translating, request)
		translating.finish()
		return
	}
	handler.ServeHTTP(recorder, request)
}

// replayRecorder keeps a replayed response's status, headers and the start
// of its body.
type replayRecorder struct {
	header    http.Header
	status    int
	body      []byte
	seen      int
	firstByte time.Time
	now       func() time.Time
	aborted   bool
}

func (r *replayRecorder) Header() http.Header { return r.header }

func (r *replayRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *replayRecorder) Write(p []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	if len(p) > 0 && r.firstByte.IsZero() {
		r.firstByte = r.now()
	}
	r.seen += len(p)
	if room := api.OutboundAIRequestContentMaxBytes + api.OutboundAICaptureRedactSlackBytes - len(r.body); room > 0 {
		r.body = append(r.body, p[:min(len(p), room)]...)
	}
	return len(p), nil
}

func (r *replayRecorder) Flush() {}

// replayAttempt is one target's request.
type replayAttempt struct {
	path, rawQuery string
	body           []byte
	translator     aiResponseTranslator
	model          string
}

var errReplayUnsupported = errors.New("outbound: the captured call cannot be replayed on this target")

// buildReplayAttempt shapes the captured request for one target, without
// streaming. A captured OpenAI chat completions, Responses or embeddings
// call can replay on any provider through the route translators; other
// calls replay on integrations of the source's provider, with the model
// replaced in the body or the path.
func buildReplayAttempt(job AIReplayJob, source, target Integration, model string) (replayAttempt, error) {
	attempt := replayAttempt{model: model}
	if attempt.model == "" {
		attempt.model = job.SourceModel
	}
	if !json.Valid(job.RequestBody) {
		return attempt, errReplayUnsupported
	}
	if (source.AIProvider == api.OutboundAIProviderOpenAI || source.AIProvider == api.OutboundAIProviderAzureOpenAI) &&
		openAIRoutePath(job.RequestPath) {
		translation, err := translateRouteRequest(target, canonicalOpenAIPath(job.RequestPath), "", withoutOpenAIStream(job.RequestBody), model)
		if err != nil {
			return attempt, err
		}
		attempt.path, attempt.rawQuery, attempt.body, attempt.translator = translation.path, translation.rawQuery, translation.body, translation.translator
		return attempt, nil
	}
	if target.AIProvider != source.AIProvider {
		return attempt, errReplayUnsupported
	}
	path, body := nonStreamingNative(job.RequestPath, job.RequestBody)
	endpoint := classifyAIEndpoint(source.AIProvider, path)
	if model != "" && model != job.SourceModel {
		if endpoint.pathModel != "" {
			escaped := url.PathEscape(endpoint.pathModel)
			if !strings.Contains(path, escaped) {
				return attempt, errReplayUnsupported
			}
			path = strings.Replace(path, escaped, url.PathEscape(model), 1)
		} else {
			rewritten, err := withModel(body, model)
			if err != nil {
				return attempt, errReplayUnsupported
			}
			body = rewritten
		}
	}
	attempt.path, attempt.body = path, body
	return attempt, nil
}

// canonicalOpenAIPath maps an OpenAI or Azure OpenAI path to the OpenAI
// path the route translators expect.
func canonicalOpenAIPath(path string) string {
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		return "/v1/chat/completions"
	case strings.HasSuffix(path, "/responses"):
		return "/v1/responses"
	default:
		return "/v1/embeddings"
	}
}

// withoutOpenAIStream turns streaming off in an OpenAI-format body; OpenAI
// refuses stream_options without a stream.
func withoutOpenAIStream(body []byte) []byte {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return body
	}
	_, streamed := fields["stream"]
	_, options := fields["stream_options"]
	if !streamed && !options {
		return body
	}
	delete(fields, "stream")
	delete(fields, "stream_options")
	rewritten, err := json.Marshal(fields)
	if err != nil {
		return body
	}
	return rewritten
}

// nonStreamingNative switches a native call to its non-streaming form.
func nonStreamingNative(path string, body []byte) (string, []byte) {
	for streaming, plain := range map[string]string{
		":streamGenerateContent": ":generateContent", ":streamRawPredict": ":rawPredict",
		"/converse-stream": "/converse", "/invoke-with-response-stream": "/invoke",
	} {
		if strings.HasSuffix(path, streaming) {
			path = strings.TrimSuffix(path, streaming) + plain
		}
	}
	return path, withoutOpenAIStream(body)
}

// problemCode reads the stable code of a gateway or provider problem.
func problemCode(body []byte, contentType string) string {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType != "application/problem+json" {
		return "provider_error"
	}
	var problem struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(body, &problem) != nil || problem.Code == "" || len(problem.Code) > 64 {
		return "provider_error"
	}
	return problem.Code
}

// redactedOutputText extracts a response's text and redacts it like
// captured content.
func redactedOutputText(body []byte, contentType string) string {
	text, _ := airedact.Redact([]byte(extractOutputText(body, contentType)), airedact.Kinds...)
	return truncateUTF8(string(text), api.OutboundAIReplayOutputTextMaxBytes)
}

// extractOutputText reads the generated text of a JSON or event-stream
// response in the OpenAI chat, completions, Responses, Anthropic, Gemini,
// Bedrock Converse or transcription format. Tool calls appear as
// "[tool name(arguments)]" and embeddings or images as a count.
func extractOutputText(body []byte, contentType string) string {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	var text strings.Builder
	switch {
	case mediaType == "text/event-stream":
		for _, line := range bytes.Split(body, []byte("\n")) {
			payload, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:"))
			if ok {
				appendOutputText(&text, bytes.TrimSpace(payload))
			}
		}
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		appendOutputText(&text, body)
	}
	return truncateUTF8(text.String(), api.OutboundAIReplayOutputTextMaxBytes)
}

type outputToolCall struct {
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type outputPart struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type outputEnvelope struct {
	Text    *string `json:"text"`
	Choices []struct {
		Text    *string `json:"text"`
		Message *struct {
			Content   json.RawMessage  `json:"content"`
			ToolCalls []outputToolCall `json:"tool_calls"`
		} `json:"message"`
		Delta *struct {
			Content   *string          `json:"content"`
			ToolCalls []outputToolCall `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Output     json.RawMessage `json:"output"`
	Content    []outputPart    `json:"content"`
	Delta      json.RawMessage `json:"delta"`
	Type       string          `json:"type"`
	Candidates []struct {
		Content struct {
			Parts []outputPart `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Data []struct {
		Embedding json.RawMessage `json:"embedding"`
	} `json:"data"`
}

func appendOutputText(text *strings.Builder, payload []byte) {
	var envelope outputEnvelope
	if len(payload) == 0 || json.Unmarshal(payload, &envelope) != nil {
		return
	}
	if envelope.Text != nil {
		text.WriteString(*envelope.Text)
	}
	for _, choice := range envelope.Choices {
		switch {
		case choice.Message != nil:
			appendContent(text, choice.Message.Content)
			appendToolCalls(text, choice.Message.ToolCalls)
		case choice.Delta != nil:
			if choice.Delta.Content != nil {
				text.WriteString(*choice.Delta.Content)
			}
			appendToolCalls(text, choice.Delta.ToolCalls)
		case choice.Text != nil:
			text.WriteString(*choice.Text)
		}
	}
	appendParts(text, envelope.Content)
	for _, candidate := range envelope.Candidates {
		appendParts(text, candidate.Content.Parts)
	}
	appendResponsesOutput(text, envelope.Output)
	appendDelta(text, envelope.Type, envelope.Delta)
	if len(envelope.Data) > 0 {
		kind := "images"
		if len(envelope.Data[0].Embedding) > 0 {
			kind = "embeddings"
		}
		text.WriteString("[" + strconv.Itoa(len(envelope.Data)) + " " + kind + "]")
	}
}

func appendContent(text *strings.Builder, raw json.RawMessage) {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		text.WriteString(value)
		return
	}
	var parts []outputPart
	if json.Unmarshal(raw, &parts) == nil {
		appendParts(text, parts)
	}
}

func appendParts(text *strings.Builder, parts []outputPart) {
	for _, part := range parts {
		switch part.Type {
		case "tool_use", "function_call":
			text.WriteString("[" + part.Name + "]")
		default:
			text.WriteString(part.Text)
		}
	}
}

func appendToolCalls(text *strings.Builder, calls []outputToolCall) {
	for _, call := range calls {
		if call.Function.Name != "" {
			text.WriteString("[" + call.Function.Name + "(" + call.Function.Arguments + ")]")
		} else {
			text.WriteString(call.Function.Arguments)
		}
	}
}

// appendResponsesOutput reads a Responses object's output items, or a
// Bedrock Converse output message.
func appendResponsesOutput(text *strings.Builder, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var items []struct {
		Type      string       `json:"type"`
		Name      string       `json:"name"`
		Arguments string       `json:"arguments"`
		Content   []outputPart `json:"content"`
	}
	if json.Unmarshal(raw, &items) == nil {
		for _, item := range items {
			if item.Type == "function_call" {
				text.WriteString("[" + item.Name + "(" + item.Arguments + ")]")
				continue
			}
			appendParts(text, item.Content)
		}
		return
	}
	var converse struct {
		Message struct {
			Content []outputPart `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &converse) == nil {
		appendParts(text, converse.Message.Content)
	}
}

// appendDelta reads Anthropic text deltas and Responses text delta events.
func appendDelta(text *strings.Builder, eventType string, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	if eventType == "response.output_text.delta" {
		var value string
		if json.Unmarshal(raw, &value) == nil {
			text.WriteString(value)
		}
		return
	}
	var delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &delta) == nil && delta.Type == "text_delta" {
		text.WriteString(delta.Text)
	}
}

// truncateUTF8 cuts text to at most limit bytes on a character boundary.
func truncateUTF8(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

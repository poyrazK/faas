package outbound

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/onebox-faas/faas/pkg/api"
)

// AIUsageBackend settles AI token usage after a response (ADR-988). It adds
// the call's tokens to the integration's UTC-day admission counter and to the
// caller/model rollup in one shared-state write.
type AIUsageBackend interface {
	RecordAIUsage(context.Context, AIUsageRecord) error
}

// aiUsageRecordAttemptTimeout bounds one settlement attempt, and
// aiUsageRecordRetryBackoff spaces the attempts. The preview soak (ADR-998)
// dropped usage at a single 2 s attempt when calls of one integration queued
// on its admission-state row; keyed retries (ADR-1120) now cover a slow
// commit within the same overall bound.
const (
	aiUsageRecordAttemptTimeout = api.OutboundAISettlementAttemptTimeoutSeconds * time.Second
	aiUsageRecordRetryBackoff   = 100 * time.Millisecond
)

// observedBody hands every byte read from the provider to the usage observer
// as the gateway forwards it. Closing it closes the provider body.
type observedBody struct {
	io.ReadCloser
	observer *aiUsageObserver
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		_, _ = b.observer.Write(p[:n])
		if b.observer.capture != nil {
			b.observer.capture.Write(p[:n])
		}
	}
	return n, err
}

// flushingWriter pushes each upstream read to the caller immediately so a
// streamed generation is not held in the server's response buffer.
type flushingWriter struct {
	dst   io.Writer
	flush func() error
}

func (w *flushingWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if err != nil {
		return n, err
	}
	if flushErr := w.flush(); flushErr != nil && !errors.Is(flushErr, http.ErrNotSupported) {
		return n, flushErr
	}
	return n, nil
}

// prepareStreamingResponse extends the write deadline of an event-stream or
// NDJSON response to the admitted request deadline, so the daemon's general
// write timeout cannot cut off a long generation. Call it before WriteHeader.
func prepareStreamingResponse(ctx context.Context, w http.ResponseWriter, contentType string) *http.ResponseController {
	if !streamingMediaType(contentType) {
		return nil
	}
	controller := http.NewResponseController(w)
	if deadline, ok := ctx.Deadline(); ok {
		_ = controller.SetWriteDeadline(deadline)
	}
	return controller
}

// observeAIResponse starts usage extraction for an AI integration's
// generation call. It returns nil when the response is not an AI call. The
// plan picks the call's wire format and the model to report when the
// response names none (ADR-991). A cache hit is observed too, to value
// what it saved (ADR-1440); its bodies are never captured.
func observeAIResponse(integration Integration, r *http.Request, resp *http.Response, cacheHit bool, ai *aiRequest, plan *aiCallPlan) *aiUsageObserver {
	if integration.AIProvider == "" || r.Method != http.MethodPost || resp == nil || resp.Body == nil ||
		(plan != nil && plan.endpoint.batch) {
		return nil
	}
	var observer *aiUsageObserver
	if plan != nil && plan.endpoint.format != aiFormatNone {
		observer = newAIUsageObserverFormat(plan.endpoint.format, resp.Header.Get("Content-Type"), time.Now)
		observer.fallbackModel = plan.facts.model
		observer.media = plan.endpoint.media
		observer.chatSearch = plan.facts.tools.chatSearch
	} else {
		observer = newAIUsageObserver(integration.AIProvider, resp.Header.Get("Content-Type"), time.Now)
	}
	observer.applyHeaders(resp.Header)
	_, replaying := replayPrincipalFromContext(r.Context())
	_, internal := internalPrincipalFromContext(r.Context())
	if integration.AIRequestLogEnabled && integration.AIRequestLogContent && !replaying && !internal && !cacheHit {
		observer.capture = newAIContentCapture(ai, resp.Header.Get("Content-Type"))
		if plan != nil {
			observer.capture.content.RequestPath = plan.path
		}
	}
	resp.Body = &observedBody{ReadCloser: resp.Body, observer: observer}
	if ai != nil && ai.usageInjected && streamingMediaType(resp.Header.Get("Content-Type")) {
		// ADR-990: the caller never asked for the usage chunk; hide it after
		// the observer has counted it.
		resp.Body = newUsageChunkFilter(resp.Body)
	}
	return observer
}

// aiSettlement is what the handler knows about a finished AI call besides
// its usage: the provider status, attempts, start time and route.
type aiSettlement struct {
	status   int
	attempts int
	started  time.Time
	routeID  string
	// endUser is the call's X-Gregale-AI-User value (ADR-994), and tags its
	// X-Gregale-AI-Tags (ADR-1436).
	endUser string
	tags    []string
	// traceID is the call's AI trace when its integration records traces
	// (ADR 20261020202035).
	traceID string
	// reserve and billable let a billable call whose usage cannot be read
	// settle at its admission estimate instead of zero (ADR-996).
	reserve  aiReservation
	billable bool
	// media and characters let a speech call settle its input characters,
	// which the binary audio response does not report (ADR-997).
	media      aiMediaKind
	characters int64
	// video is a video call's seconds and resolution (ADR-1432).
	videoSeconds    int64
	videoResolution string
	// codeGB is the memory a code-interpreter session is charged at
	// (ADR-1433).
	codeGB int64
	// leaseID keys the settlement (ADR-1120).
	leaseID string
	// route places a route attempt in its route, and moderation is the
	// call's moderation result (ADR-1340).
	route      routeAttempt
	moderation aiModerationOutcome
	// response is the response check's result, filled as the response is
	// sent (ADR-1435); nil when the response is not checked.
	response *aiResponseCheck
	// cacheHit marks a response the cache served (ADR-1440).
	cacheHit bool
}

// speechUsage settles a successful speech call at its input characters when
// the model is priced per character. Other speech calls stay unknown and
// settle at their reservation.
func speechUsage(integration Integration, settlement aiSettlement, status int, usage AIUsage,
	outcome AIUsageOutcome) (AIUsage, AIUsageOutcome) {
	if settlement.media != aiMediaSpeech || outcome != AIUsageOutcomeUnknown || status < 200 || status > 299 {
		return usage, outcome
	}
	if price, ok := aiPriceFor(integration, usage.Model); ok && price.Characters > 0 {
		return AIUsage{Model: usage.Model, Known: true, Characters: settlement.characters}, AIUsageOutcomeKnown
	}
	return usage, outcome
}

// videoUsage settles a created video at the seconds it asked for: the
// provider bills them, and the operation it returns reports no usage
// (ADR-1432).
func videoUsage(settlement aiSettlement, status int, usage AIUsage, outcome AIUsageOutcome) (AIUsage, AIUsageOutcome) {
	if settlement.media != aiMediaVideo || status < 200 || status > 299 {
		return usage, outcome
	}
	return AIUsage{Model: usage.Model, Known: true, VideoSeconds: settlement.videoSeconds,
		VideoResolution: settlement.videoResolution}, AIUsageOutcomeKnown
}

// aiContainerSessions remembers the code-interpreter containers this
// process charged, so a container a conversation reuses is charged once per
// session window (ADR-1433). It is bounded and starts over when full.
type aiContainerSessions struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// aiContainerSessionsMax bounds the remembered containers.
const aiContainerSessionsMax = 10_000

// charge returns how many of a response's containers start a session.
func (c *aiContainerSessions) charge(integrationID string, ids []string, now time.Time) int64 {
	if len(ids) == 0 {
		return 0
	}
	window := api.OutboundAIContainerSessionMinutes * time.Minute
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil || len(c.seen) >= aiContainerSessionsMax {
		c.seen = make(map[string]time.Time)
	}
	var sessions int64
	for _, id := range ids {
		key := integrationID + "/" + id
		if started, ok := c.seen[key]; ok && now.Sub(started) < window {
			continue
		}
		c.seen[key] = now
		sessions++
	}
	return sessions
}

// estimatedAIUsage is what a billable call with unreadable usage settles
// at: its reservation, priced as reserved. Errors and non-billable calls
// keep zero.
func estimatedAIUsage(outcome AIUsageOutcome, usage AIUsage, settlement aiSettlement) (AIUsage, int64, bool) {
	if outcome != AIUsageOutcomeUnknown || !settlement.billable || (settlement.reserve.tokens <= 0 && settlement.reserve.cost <= 0) {
		return usage, 0, false
	}
	estimate := settlement.reserve.estimate
	estimate.Model, estimate.Known = usage.Model, false
	return estimate, settlement.reserve.cost, true
}

// settleAIUsage records one finished AI call. It runs after the response copy,
// including an aborted one, so partial streams still count what they used.
func (h *Handler) settleAIUsage(ctx context.Context, integration Integration, appID string, settlement aiSettlement,
	observer *aiUsageObserver, span oteltrace.Span, metricIntegrationID string) {
	if settlement.cacheHit {
		h.settleAICacheHit(ctx, integration, appID, settlement, observer, span, metricIntegrationID)
		return
	}
	usage := observer.Finish()
	status, upstreamStarted := settlement.status, settlement.started
	outcome := aiUsageOutcome(status, usage)
	if outcome == AIUsageOutcomeError {
		usage = AIUsage{Model: usage.Model}
	}
	var timeToFirstByte time.Duration
	if first := observer.FirstByteAt(); !first.IsZero() && first.After(upstreamStarted) {
		timeToFirstByte = first.Sub(upstreamStarted)
	}
	usage, outcome = speechUsage(integration, settlement, status, usage, outcome)
	usage, outcome = videoUsage(settlement, status, usage, outcome)
	if outcome != AIUsageOutcomeError {
		// ADR-1433: a container is charged once per session window.
		usage.CodeSessionGB = h.aiContainers.charge(integration.ID, observer.containerIDs(), time.Now()) * max(settlement.codeGB, 1)
	}
	cost := aiCallCostMillicents(integration, usage)
	if estimated, estimatedCost, ok := estimatedAIUsage(outcome, usage, settlement); ok {
		usage, cost = estimated, estimatedCost
		span.SetAttributes(attribute.Bool("gregale.ai.usage_estimated", true))
	}
	settled := settledAIUsage{usage: usage, outcome: outcome, cost: cost, timeToFirstByte: timeToFirstByte, capture: observer.capture}
	if outcome != AIUsageOutcomeError {
		settled.toolCalls, settled.toolSignature = observer.toolCalls()
	}
	h.recordSettledAIUsage(ctx, integration, appID, settlement, settled, span, metricIntegrationID)
}

// settledAIUsage is one call's usage as it is recorded.
type settledAIUsage struct {
	usage           AIUsage
	outcome         AIUsageOutcome
	cost            int64
	timeToFirstByte time.Duration
	capture         *aiContentCapture
	// toolCalls and toolSignature are the tool calls the response requested
	// (ADR 20261020202035).
	toolCalls     []string
	toolSignature []byte
}

// recordSettledAIUsage counts and writes one call's settled usage: metrics,
// span attributes, the usage rollup, the request log and the log export.
// Ordinary calls and realtime responses (ADR 20261019072722) share it.
func (h *Handler) recordSettledAIUsage(ctx context.Context, integration Integration, appID string, settlement aiSettlement,
	settled settledAIUsage, span oteltrace.Span, metricIntegrationID string) {
	usage, outcome, cost, timeToFirstByte := settled.usage, settled.outcome, settled.cost, settled.timeToFirstByte
	h.Metrics.ObserveAIUsage(metricIntegrationID, outcome, usage, timeToFirstByte)
	h.Metrics.ObserveAICost(metricIntegrationID, cost)
	span.SetAttributes(aiSpanAttributes(integration.AIProvider, outcome, usage, timeToFirstByte)...)
	if cost > 0 {
		span.SetAttributes(attribute.Int64("gregale.ai.cost_millicents", cost))
	}
	if settlement.response != nil && settlement.response.result != "" {
		span.SetAttributes(attribute.String("gregale.ai.response_check", settlement.response.result))
	}
	recorder, ok := h.Backend.(AIUsageBackend)
	if !ok {
		h.Metrics.ObserveAIUsageRecord(metricIntegrationID, "unavailable")
		return
	}
	caller := appID
	replay, replaying := replayPrincipalFromContext(ctx)
	internal, isInternal := internalPrincipalFromContext(ctx)
	switch {
	case isInternal:
		caller = internal.caller
		if internal.outcome != nil {
			*internal.outcome = replayOutcome{settled: true, outcome: outcome, usage: usage, cost: cost}
		}
	case replaying:
		caller = AIUsageCallerReplay
		if replay.outcome != nil {
			*replay.outcome = replayOutcome{settled: true, outcome: outcome, usage: usage, cost: cost}
		}
	case caller == "":
		caller = AIUsageCallerRuns
	}
	record := AIUsageRecord{
		IntegrationID: integration.ID, Caller: caller, Outcome: outcome, Usage: usage, CostMillicents: cost,
		EndUser: settlement.endUser, Tags: settlement.tags, LeaseID: settlement.leaseID,
		PromptCacheSavedMillicents: promptCacheSavedMillicents(integration, usage, cost),
		TraceID:                    settlement.traceID, ToolCalls: settled.toolCalls, ToolSignature: settled.toolSignature,
	}
	if integration.AIRequestLogEnabled {
		record.Log = newAIRequestLog(settlement, timeToFirstByte, time.Now())
		if settled.capture != nil {
			record.Log.Content = settled.capture.finish()
		}
	}
	if settlement.route.tracked {
		record.Route = aiRouteAttemptFor(settlement.route, outcome, timeToFirstByte)
	}
	record.Export = aiLogExportFor(integration, record, settlement, timeToFirstByte, time.Now())
	write := func(ctx context.Context) ([]AIBudgetAlert, error) {
		if alerter, ok := h.Backend.(AIBudgetAlertRecorder); ok {
			return alerter.RecordAIUsageAlerts(ctx, record)
		}
		return nil, recorder.RecordAIUsage(ctx, record)
	}
	alerts, err := h.recordAIUsage(context.WithoutCancel(ctx), record, metricIntegrationID, write)
	if err != nil {
		h.Metrics.ObserveAIUsageRecord(metricIntegrationID, "state_error")
		return
	}
	for _, alert := range alerts {
		h.Metrics.ObserveAIBudgetAlert(alert.Budget, alert.ThresholdPercent)
	}
	if record.Export != nil {
		h.Metrics.ObserveAILogExport(metricIntegrationID)
	}
	h.Metrics.ObserveAIUsageRecord(metricIntegrationID, "recorded")
}

// settleAICacheHit records a response the cache served (ADR-1440): one
// cached request of the caller and model, with no tokens and no cost, and
// what the provider call would have cost, priced from the cached response's
// usage. Nothing was spent, so caps, end users and tags are not touched.
func (h *Handler) settleAICacheHit(ctx context.Context, integration Integration, appID string, settlement aiSettlement,
	observer *aiUsageObserver, span oteltrace.Span, metricIntegrationID string) {
	usage := observer.Finish()
	saved := aiCallCostMillicents(integration, usage)
	span.SetAttributes(attribute.Int64("gregale.ai.cache_saved_millicents", saved))
	h.Metrics.ObserveAIResponseCacheSaved(metricIntegrationID, saved)
	recorder, ok := h.Backend.(AIUsageBackend)
	if !ok {
		h.Metrics.ObserveAIUsageRecord(metricIntegrationID, "unavailable")
		return
	}
	caller := appID
	if caller == "" {
		caller = AIUsageCallerRuns
	}
	record := AIUsageRecord{IntegrationID: integration.ID, Caller: caller, Outcome: AIUsageOutcomeKnown,
		Usage: AIUsage{Model: usage.Model, Known: true}, Cached: true, CacheSavedMillicents: saved, LeaseID: settlement.leaseID}
	write := func(ctx context.Context) ([]AIBudgetAlert, error) { return nil, recorder.RecordAIUsage(ctx, record) }
	if _, err := h.recordAIUsage(context.WithoutCancel(ctx), record, metricIntegrationID, write); err != nil {
		h.Metrics.ObserveAIUsageRecord(metricIntegrationID, "state_error")
		return
	}
	h.Metrics.ObserveAIUsageRecord(metricIntegrationID, "recorded")
}

// promptCacheSavedMillicents is what provider prompt caching saved on one
// call: its cost with cached reads and cache writes priced as plain input,
// minus its cost. It is negative when writes outweigh reads (ADR-1440).
func promptCacheSavedMillicents(integration Integration, usage AIUsage, cost int64) int64 {
	if !usage.Known || usage.CachedInputTokens+usage.CacheWriteTokens == 0 {
		return 0
	}
	plain := usage
	plain.CachedInputTokens, plain.CacheWriteTokens, plain.CachedInputAudioTokens = 0, 0, 0
	return aiCallCostMillicents(integration, plain) - cost
}

// aiRouteAttemptFor is a canary-tracked attempt's contribution to its
// target's per-minute row: an error for any provider status of 400 or
// above, and the time to first byte of a successful attempt (ADR-1340). A
// route without a UUID (a static test route) has no row, so the attempt
// never makes its settlement invalid.
func aiRouteAttemptFor(attempt routeAttempt, outcome AIUsageOutcome, timeToFirstByte time.Duration) *AIRouteAttemptRecord {
	if !validUUID(attempt.routeID) {
		return nil
	}
	record := &AIRouteAttemptRecord{RouteID: attempt.routeID, Position: attempt.position, Error: outcome == AIUsageOutcomeError}
	if !record.Error && timeToFirstByte > 0 {
		ttfb := timeToFirstByte.Milliseconds()
		record.TTFBMS = &ttfb
	}
	return record
}

// recordAIUsage writes one settlement, retrying a failed attempt when the
// call has a lease to key it: a timed-out commit may still land, and the
// key makes the retry count the call once (ADR-1120). An invalid record is
// never retried.
func (h *Handler) recordAIUsage(ctx context.Context, record AIUsageRecord, metricIntegrationID string,
	write func(context.Context) ([]AIBudgetAlert, error)) ([]AIBudgetAlert, error) {
	for attempt := 1; ; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, aiUsageRecordAttemptTimeout)
		alerts, err := write(attemptCtx)
		cancel()
		if err == nil || attempt >= api.OutboundAISettlementAttempts || record.LeaseID == "" || errors.Is(err, ErrInvalidIntegration) {
			return alerts, err
		}
		h.Metrics.ObserveAIUsageRecord(metricIntegrationID, "retried")
		time.Sleep(time.Duration(attempt) * aiUsageRecordRetryBackoff)
	}
}

// observeContentPolicy counts the content policy's redactions and block and
// names the redactions to the caller (ADR-997).
func (h *Handler) observeContentPolicy(w http.ResponseWriter, metricIntegrationID string, scan *contentScan) {
	if scan == nil {
		return
	}
	if scan.blocked != "" {
		h.Metrics.ObserveAIContent(metricIntegrationID, scan.blocked, "blocked")
	}
	for kind, count := range scan.redacted {
		for range count {
			h.Metrics.ObserveAIContent(metricIntegrationID, string(kind), "redacted")
		}
	}
	if len(scan.redacted) > 0 {
		w.Header().Set(AIContentRedactionsHeader, formatRedactions(scan.redacted))
	}
}

// aiSpanAttributes follows the OpenTelemetry GenAI semantic conventions for
// the provider, response model and token usage.
func aiSpanAttributes(provider string, outcome AIUsageOutcome, usage AIUsage, timeToFirstByte time.Duration) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.system", provider),
		attribute.String("gen_ai.response.model", usage.Model),
		attribute.String("gregale.ai.usage_outcome", string(outcome)),
	}
	if outcome == AIUsageOutcomeKnown {
		attrs = append(attrs,
			attribute.Int64("gen_ai.usage.input_tokens", usage.InputTokens),
			attribute.Int64("gen_ai.usage.output_tokens", usage.OutputTokens),
			attribute.Int64("gen_ai.usage.cache_read.input_tokens", usage.CachedInputTokens),
			attribute.Int64("gen_ai.usage.cache_creation.input_tokens", usage.CacheWriteTokens),
		)
	}
	if timeToFirstByte > 0 {
		attrs = append(attrs, attribute.Int64("gregale.ai.time_to_first_byte_ms", timeToFirstByte.Milliseconds()))
	}
	return attrs
}

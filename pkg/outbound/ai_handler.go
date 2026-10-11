package outbound

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// AIUsageBackend settles AI token usage after a response (ADR-979). It adds
// the call's tokens to the integration's UTC-day admission counter and to the
// caller/model rollup in one shared-state write.
type AIUsageBackend interface {
	RecordAIUsage(context.Context, AIUsageRecord) error
}

const aiUsageRecordTimeout = 2 * time.Second

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
// response names none (ADR-984).
func observeAIResponse(integration Integration, r *http.Request, resp *http.Response, cacheHit bool, ai *aiRequest, plan *aiCallPlan) *aiUsageObserver {
	if integration.AIProvider == "" || r.Method != http.MethodPost || cacheHit || resp == nil || resp.Body == nil {
		return nil
	}
	var observer *aiUsageObserver
	if plan != nil && plan.endpoint.format != aiFormatNone {
		observer = newAIUsageObserverFormat(plan.endpoint.format, resp.Header.Get("Content-Type"), time.Now)
		observer.fallbackModel = plan.facts.model
	} else {
		observer = newAIUsageObserver(integration.AIProvider, resp.Header.Get("Content-Type"), time.Now)
	}
	observer.applyHeaders(resp.Header)
	resp.Body = &observedBody{ReadCloser: resp.Body, observer: observer}
	if ai != nil && ai.usageInjected && streamingMediaType(resp.Header.Get("Content-Type")) {
		// ADR-983: the caller never asked for the usage chunk; hide it after
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
}

// settleAIUsage records one finished AI call. It runs after the response copy,
// including an aborted one, so partial streams still count what they used.
func (h *Handler) settleAIUsage(ctx context.Context, integration Integration, appID string, settlement aiSettlement,
	observer *aiUsageObserver, span oteltrace.Span, metricIntegrationID string) {
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
	cost := aiCostMillicents(integration.AIPrices, usage)
	h.Metrics.ObserveAIUsage(metricIntegrationID, outcome, usage, timeToFirstByte)
	h.Metrics.ObserveAICost(metricIntegrationID, cost)
	span.SetAttributes(aiSpanAttributes(integration.AIProvider, outcome, usage, timeToFirstByte)...)
	if cost > 0 {
		span.SetAttributes(attribute.Int64("gregale.ai.cost_millicents", cost))
	}
	recorder, ok := h.Backend.(AIUsageBackend)
	if !ok {
		h.Metrics.ObserveAIUsageRecord(metricIntegrationID, "unavailable")
		return
	}
	caller := appID
	if caller == "" {
		caller = AIUsageCallerRuns
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), aiUsageRecordTimeout)
	defer cancel()
	record := AIUsageRecord{
		IntegrationID: integration.ID, Caller: caller, Outcome: outcome, Usage: usage, CostMillicents: cost,
	}
	if integration.AIRequestLogEnabled {
		record.Log = newAIRequestLog(settlement, timeToFirstByte, time.Now())
	}
	if err := recorder.RecordAIUsage(recordCtx, record); err != nil {
		h.Metrics.ObserveAIUsageRecord(metricIntegrationID, "state_error")
		return
	}
	h.Metrics.ObserveAIUsageRecord(metricIntegrationID, "recorded")
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

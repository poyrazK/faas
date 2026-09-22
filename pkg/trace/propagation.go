package trace

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// InjectHeaders returns the bounded platform propagation envelope stored on
// durable queue messages. The W3C fields are injected by the configured global
// propagator; the canonical Gregale trace id is added for indexed lookup.
func InjectHeaders(ctx context.Context) map[string]string {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	if spanContext := SpanFromContext(ctx).SpanContext(); spanContext.IsValid() {
		carrier[api.TraceIDHeader] = spanContext.TraceID().String()
	}
	return map[string]string(carrier)
}

// MergeHeaders returns a durable invocation header envelope that preserves
// caller headers while making the platform-owned W3C context authoritative.
// Trace context is security-sensitive: accepting a caller-supplied
// traceparent would let one tenant make an invocation appear in another
// request's trace, and persisting unbounded baggage would turn a small
// invocation into a durable header-amplification path.
func MergeHeaders(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	headers := make(map[string]string)
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &headers); err != nil {
			return nil, err
		}
		if headers == nil {
			headers = make(map[string]string)
		}
	}
	return json.Marshal(MergeHeaderMap(ctx, headers))
}

// MergeHeaderMap is the map form of MergeHeaders for internal producers that
// already have structured metadata (for example event fan-out and cron).
// Existing platform propagation keys are removed case-insensitively before
// the trusted context is injected.
func MergeHeaderMap(ctx context.Context, headers map[string]string) map[string]string {
	// Use only the caller-controlled map length as the capacity hint. Adding a
	// fixed propagation allowance here can overflow an int before allocation;
	// the map grows naturally for the small platform-owned header set below.
	merged := make(map[string]string, len(headers))
	for key, value := range headers {
		if isPlatformPropagationHeader(key) {
			continue
		}
		merged[key] = value
	}
	for key, value := range InjectHeaders(ctx) {
		merged[key] = value
	}
	return merged
}

// PropagateHeaders extracts a producer's W3C context and re-injects it into
// a fresh envelope. The fallback context remains the scheduler/request
// context when the producer did not supply a valid traceparent.
func PropagateHeaders(ctx context.Context, incoming map[string]string) map[string]string {
	return MergeHeaderMap(ExtractHeaders(ctx, incoming), nil)
}

func isPlatformPropagationHeader(key string) bool {
	switch strings.ToLower(key) {
	case "traceparent", "tracestate", "baggage", strings.ToLower(api.TraceIDHeader):
		return true
	default:
		return false
	}
}

// ExtractHeaders restores a queue producer's W3C context before a delivery
// span is created. Unknown fields remain ignored by the OTel propagator.
func ExtractHeaders(ctx context.Context, headers map[string]string) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(headers))
}

package gateway

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
)

// Keep customer-controlled propagation metadata small at the guest boundary.
// TraceState has a 512-byte W3C limit; baggage is deliberately capped below
// the OTel parser's 8 KiB limit so it cannot dominate the init frame or raw
// request head sent over the vmmd bridge.
const (
	maxGuestTraceStateBytes = 512
	maxGuestBaggageBytes    = 2048
	maxGuestBaggageMembers  = 16
)

// injectGuestTraceContext copies the current request's W3C context into the
// guest-bound carrier. Trace context is injected on every request, including
// requests handled by a warm instance. Baggage is request-scoped too, but is
// only forwarded when it fits the guest-boundary budget; it is never stamped
// into the runner environment, which is process-scoped and cannot represent
// concurrent requests safely.
func injectGuestTraceContext(ctx context.Context, headers http.Header) {
	carrier := propagation.HeaderCarrier(headers)
	propagation.TraceContext{}.Inject(ctx, carrier)
	if len(headers.Get("tracestate")) > maxGuestTraceStateBytes {
		headers.Del("tracestate")
	}

	// A cloned inbound header must not bypass the baggage budget when the
	// request has no valid baggage in its OTel context.
	headers.Del("baggage")
	b := baggage.FromContext(ctx)
	if b.Len() == 0 || b.Len() > maxGuestBaggageMembers {
		return
	}
	raw := b.String()
	if len(raw) == 0 || len(raw) > maxGuestBaggageBytes {
		return
	}
	headers.Set("baggage", raw)
}

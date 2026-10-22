// adr: 1138
package gateway

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type traceInferredKey struct{}

// withInferredCallerTrace links a service call that carries no valid trace
// context to the trace of the one request its calling instance is serving
// (ADR-1138): it sets that traceparent on the request, as if the caller had
// forwarded it, and marks the request so the span and telemetry record the
// link as inferred. A traceparent the caller sends always wins.
func (p *ServiceProxy) withInferredCallerTrace(r *http.Request) *http.Request {
	if p.inferCallerTrace == nil || hasValidTraceparent(r) {
		return r
	}
	traceparent := p.inferCallerTrace(r.Context(), r.RemoteAddr)
	if traceparent == "" {
		return r
	}
	carrier := propagation.HeaderCarrier(http.Header{"Traceparent": []string{traceparent}})
	if !oteltrace.SpanContextFromContext(propagation.TraceContext{}.Extract(r.Context(), carrier)).IsValid() {
		return r
	}
	r.Header.Set("Traceparent", traceparent)
	// Vendor state belonged to whatever the caller half-sent; it is not part
	// of the inferred parent.
	r.Header.Del("Tracestate")
	return r.WithContext(context.WithValue(r.Context(), traceInferredKey{}, true))
}

func hasValidTraceparent(r *http.Request) bool {
	ctx := propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	return oteltrace.SpanContextFromContext(ctx).IsRemote()
}

// traceInferredOf reports whether the request's trace context was inferred.
func traceInferredOf(r *http.Request) bool {
	inferred, _ := r.Context().Value(traceInferredKey{}).(bool)
	return inferred
}

// adr: 1138, 20261102082606
package gateway

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/onebox-faas/faas/pkg/api"
)

type traceInferredKey struct{}

// InferredCallerRequest is what the local vmmd knows of the one request a
// calling instance is serving: its traceparent (ADR-1138) and when its
// request budget runs out (ADR-20261102082606). Either may be empty.
type InferredCallerRequest struct {
	Traceparent string
	Deadline    time.Time
}

// withInferredCaller asks the local vmmd, at most once, about the one request
// the calling instance is serving, when the call lacks what vmmd can supply:
// a valid traceparent, or (with caller deadlines on) a forwarded
// X-Faas-Deadline-Ms. It links the call to the inferred trace and returns the
// inferred deadline, zero when unknown.
func (p *ServiceProxy) withInferredCaller(r *http.Request) (*http.Request, time.Time) {
	needTrace := !hasValidTraceparent(r)
	needDeadline := p.callerDeadlines && forwardedCallerBudget(r) == 0
	if p.inferCallerRequest == nil || (!needTrace && !needDeadline) {
		return r, time.Time{}
	}
	inferred := p.inferCallerRequest(r.Context(), r.RemoteAddr)
	if needTrace {
		r = withInferredCallerTrace(r, inferred.Traceparent)
	}
	return r, inferred.Deadline
}

// withInferredCallerTrace links a service call that carries no valid trace
// context to the trace of the one request its calling instance is serving
// (ADR-1138): it sets that traceparent on the request, as if the caller had
// forwarded it, and marks the request so the span and telemetry record the
// link as inferred. A traceparent the caller sends always wins.
func withInferredCallerTrace(r *http.Request, traceparent string) *http.Request {
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

// forwardedCallerBudget is the request budget the caller's code forwarded in
// X-Faas-Deadline-Ms on a service call, or 0 when it sent none or an invalid
// one. It can only shorten the call (ADR-20261102082606).
func forwardedCallerBudget(r *http.Request) time.Duration {
	values := r.Header.Values(api.DeadlineHeader)
	if len(values) != 1 {
		return 0
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(values[0]), 10, 64)
	if err != nil || ms <= 0 || ms > api.RequestBudgetPlanMax.Milliseconds() {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// callerDeadline is when the calling request's budget runs out: the budget
// the caller forwarded, counted from the call's arrival, or else the deadline
// vmmd inferred. ok is false when neither is known.
func callerDeadline(r *http.Request, inferred, started time.Time) (time.Time, bool) {
	if budget := forwardedCallerBudget(r); budget > 0 {
		return started.Add(budget), true
	}
	return inferred, !inferred.IsZero()
}

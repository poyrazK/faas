package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// requestBudgetExpired reports the gateway-owned deadline, rather than a
// client disconnect. A request budget is attached by the edge handler after
// app resolution; checking both the context error and the budget marker keeps
// ordinary transport cancellations from being turned into customer-visible
// timeout responses.
func requestBudgetExpired(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	b, ok := reqbudget.FromContext(ctx)
	if !ok {
		return false
	}
	// Transports can return EOF at the same instant the budget timer fires,
	// before the context's Err field is observable to this goroutine. The
	// budget's own wall-clock calculation is the authoritative fallback.
	return errors.Is(ctx.Err(), context.DeadlineExceeded) || b.Remaining(time.Time{}) <= 0
}

// writeRequestBudgetExceededForRequest emits the canonical timeout envelope
// plus stable edge metadata. The metadata is intentionally outside the JSON
// body: a Cloudflare Worker can preserve/reconstruct the body while
// distinguishing this platform-owned timeout from a genuine CDN failure.
func writeRequestBudgetExceededForRequest(w http.ResponseWriter, r *http.Request) {
	if r != nil {
		recordTrafficRefusal(r.Context(), "deadline")
	}
	markRequestBudgetError(w)
	ctx, cancel := context.WithTimeout(context.Background(), api.RequestBudgetErrorWriteTimeout)
	defer cancel()
	defer guardResponseWrites(ctx, w)() //nolint:contextcheck // best-effort error delivery has its own bounded allowance after the request has expired.
	// Avoid Header.Add here. gatewayd-internal stamps the request id before
	// this path and duplicate correlation headers make clients disagree about
	// which value to log.
	if r != nil {
		if rid := requestIDFrom(r); rid != "" {
			w.Header().Set(api.RequestIDHeader, rid)
		}
	}
	w.Header().Set(api.ErrorCodeHeader, api.CodeRequestBudgetExceeded)
	w.Header().Set("Cache-Control", "no-store")
	problem := api.NewProblem(http.StatusGatewayTimeout,
		api.CodeRequestBudgetExceeded,
		"Request budget exceeded",
		"the request exceeded its configured wall-clock budget")
	// Limit errors carry the limit, the observed value and a docs link
	// (CLAUDE.md conventions) so a customer can tell a 30 s budget from an
	// outage and knows which knob to change.
	if r != nil {
		if b, ok := reqbudget.FromContext(r.Context()); ok && b.Total > 0 {
			observed := time.Since(b.Started)
			if b.Started.IsZero() {
				observed = b.Total
			}
			problem = problem.WithLimit(b.Total.Milliseconds(), observed.Milliseconds())
		}
	}
	api.WriteProblem(w, problem.WithDocs(requestBudgetDocsURL))
}

// requestBudgetDocsURL is the customer errors page entry for the budget.
var requestBudgetDocsURL = docsTypeBase

// writeBurstCapacityError maps an admission wait failure without confusing a
// caller disconnect with a platform failure. It returns false when the client
// has already gone away and no response should be written.
func writeBurstCapacityError(w http.ResponseWriter, r *http.Request, err error) bool {
	if r != nil && trafficRevocationCause(r.Context()) != nil {
		writeTrafficRevocationError(w, r, trafficRevocationCause(r.Context()))
		return true
	}
	if r != nil && requestBudgetExpired(r.Context()) {
		writeRequestBudgetExceededForRequest(w, r)
		return true
	}
	if r != nil && errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		return false
	}
	writeWakeError(w, err)
	return true
}

// handleForwardRequestCancellation consumes transport errors caused by the
// inbound request context. The stream layer otherwise sees a gRPC Canceled
// status and incorrectly classifies the gateway's own deadline as Bad Gateway.
// When the response has not started, a platform budget expiry gets the same
// stable 504 envelope as the outer budget middleware.
func handleForwardRequestCancellation(w http.ResponseWriter, r *http.Request, canWrite bool) bool {
	if r == nil {
		return false
	}
	ctx := r.Context()
	if cause := trafficRevocationCause(ctx); cause != nil {
		if canWrite {
			writeTrafficRevocationError(w, r, cause)
		}
		return true
	}
	if requestBudgetExpired(ctx) {
		recordTrafficRefusal(ctx, "deadline")
		if canWrite {
			writeRequestBudgetExceededForRequest(w, r)
		}
		return true
	}
	return ctx.Err() != nil
}

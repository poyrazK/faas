package gateway

import (
	"context"
	"errors"
	"net/http"

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
	if _, ok := reqbudget.FromContext(ctx); !ok {
		return false
	}
	return errors.Is(ctx.Err(), context.DeadlineExceeded)
}

func writeRequestBudgetExceeded(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout,
		api.CodeRequestBudgetExceeded,
		"Request budget exceeded",
		"the request exceeded its wall-clock budget while capacity was becoming ready"))
}

// writeBurstCapacityError maps an admission wait failure without confusing a
// caller disconnect with a platform failure. It returns false when the client
// has already gone away and no response should be written.
func writeBurstCapacityError(w http.ResponseWriter, r *http.Request, err error) bool {
	if r != nil && requestBudgetExpired(r.Context()) {
		writeRequestBudgetExceeded(w)
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
	if r == nil || r.Context().Err() == nil {
		return false
	}
	if canWrite && requestBudgetExpired(r.Context()) {
		writeRequestBudgetExceeded(w)
	}
	return true
}

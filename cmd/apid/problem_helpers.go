package main

import (
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// writeCustomerInternalProblem keeps dependency and storage error text in
// logs while returning customer-safe recovery guidance.
func writeCustomerInternalProblem(w http.ResponseWriter, r *http.Request, log *slog.Logger, operation, detail, hint string, cause error) {
	api.WriteProblemForRequest(w, r, customerInternalProblem(log, operation, detail, hint, cause))
}

func customerInternalProblem(log *slog.Logger, operation, detail, hint string, cause error) *api.Problem {
	logCustomerFailure(log, operation, cause)
	return api.ErrInternal(detail).WithHint(hint)
}

func customerCapacityProblem(log *slog.Logger, operation, title, detail, hint string, cause error) *api.Problem {
	logCustomerFailure(log, operation, cause)
	return api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, title, detail).
		WithHeader("Retry-After", "5").
		WithHint(hint).
		WithDocs("https://gregale.dev/status")
}

func logCustomerFailure(log *slog.Logger, operation string, cause error) {
	if log != nil && cause != nil {
		log.Error("customer request failed", "operation", operation, "err", cause)
	}
}

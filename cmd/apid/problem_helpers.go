package main

import (
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
)

// Facades over pkg/apid/httpkit (ADR-982); new and lifted code calls httpkit
// directly.

// writeCustomerInternalProblem keeps dependency and storage error text in
// logs while returning customer-safe recovery guidance.
func writeCustomerInternalProblem(w http.ResponseWriter, r *http.Request, log *slog.Logger, operation, detail, hint string, cause error) {
	httpkit.WriteCustomerInternalProblem(w, r, log, operation, detail, hint, cause)
}

func customerInternalProblem(log *slog.Logger, operation, detail, hint string, cause error) *api.Problem {
	return httpkit.CustomerInternalProblem(log, operation, detail, hint, cause)
}

func customerCapacityProblem(log *slog.Logger, operation, title, detail, hint string, cause error) *api.Problem {
	return httpkit.CustomerCapacityProblem(log, operation, title, detail, hint, cause)
}

func logCustomerFailure(log *slog.Logger, operation string, cause error) {
	httpkit.LogCustomerFailure(log, operation, cause)
}

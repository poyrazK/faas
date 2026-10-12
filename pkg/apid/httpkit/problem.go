package httpkit

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// NotFound writes a 404 problem, distinguishing missing rows.
func NotFound(w http.ResponseWriter, what string) {
	api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", what))
}

// NoSuchRoute writes the 404 apid returns for an unregistered /v1 route.
// Routes hidden from customers reuse it so they look exactly like missing ones.
func NoSuchRoute(w http.ResponseWriter) { NotFound(w, "no such API route") }

// WriteCustomerInternalProblem keeps dependency and storage error text in
// logs while returning customer-safe recovery guidance.
func WriteCustomerInternalProblem(w http.ResponseWriter, r *http.Request, log *slog.Logger, operation, detail, hint string, cause error) {
	api.WriteProblemForRequest(w, r, CustomerInternalProblem(log, operation, detail, hint, cause))
}

// CustomerInternalProblem logs cause and returns a customer-safe 500, unless
// cause is a route-removal/lifecycle gate, which keeps its 409.
func CustomerInternalProblem(log *slog.Logger, operation, detail, hint string, cause error) *api.Problem {
	if p := RouteRemovalBlockedProblem(cause); p != nil {
		return p
	}
	LogCustomerFailure(log, operation, cause)
	return api.ErrInternal(detail).WithHint(hint)
}

// CustomerCapacityProblem logs cause and returns a retryable 503, unless
// cause is a route-removal/lifecycle gate, which keeps its 409.
func CustomerCapacityProblem(log *slog.Logger, operation, title, detail, hint string, cause error) *api.Problem {
	if p := RouteRemovalBlockedProblem(cause); p != nil {
		return p
	}
	LogCustomerFailure(log, operation, cause)
	return api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, title, detail).
		WithHeader("Retry-After", "5").
		WithHint(hint).
		WithDocs("https://gregale.dev/status")
}

// LogCustomerFailure records a failed customer request with sanitized fields.
// A nil logger or nil cause is a no-op.
func LogCustomerFailure(log *slog.Logger, operation string, cause error) {
	if log != nil && cause != nil {
		log.Error("customer request failed", "operation", logsanitize.Field(operation), "err", logsanitize.FieldAny(cause))
	}
}

// RouteRemovalBlockedProblem maps route lifecycle and route-removal policy
// failures (typed state errors or their Postgres constraint violations) to a
// 409 problem. It returns nil for any other error.
func RouteRemovalBlockedProblem(err error) *api.Problem {
	var gate *state.RouteGateBlockedError
	var lifecycleDB *pgconn.PgError
	if errors.As(err, &gate) {
		return api.NewProblem(http.StatusConflict, api.CodeRouteGateBlocked, "Route lifecycle blocked traffic", strings.Join(gate.Decision.Reasons, ", ")).WithHint("Resolve lifecycle findings and approve any changed successor for the exact captures before retrying production traffic.")
	}
	if errors.As(err, &lifecycleDB) && lifecycleDB.ConstraintName == "production_lifecycle_required" {
		return api.NewProblem(http.StatusConflict, api.CodeRouteGateBlocked, "Route lifecycle review required", "The transaction has no current lifecycle authorization.").WithHint("Refresh capture and lifecycle review, then retry the production traffic change.")
	}

	var blocked *state.RouteRemovalBlockedError
	if !errors.As(err, &blocked) {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "route_removal_required" {
			return nil
		}
		blocked = &state.RouteRemovalBlockedError{Reason: pgErr.Detail}
	}
	return api.NewProblem(http.StatusConflict, api.CodeRouteRemovalRequired, "Route removal policy blocked traffic", strings.TrimSpace(blocked.Reason)).WithHint("Read /v1/apps/{slug}/route-removal/check?deployment_id=UUID; obtain a fresh authenticated approval for the exact production baseline, candidate contracts and successor mapping before increasing traffic.")
}

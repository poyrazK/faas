package api

import (
	"errors"
	"fmt"
)

// APIError carries a server problem so callers can type-switch on it
// and render the RFC 7807 envelope verbatim. Every Client.do on the
// SDK returns an *APIError for 4xx/5xx with a Problem-shaped body; the
// renderer can choose its own copy.
//
// This is a thin wrapper around the canonical Problem type
// (pkg/api/errors.go). Command-line tools may want three-line UX
// rendering; HTTP middleware may want just to re-emit; the SDK only
// owns the carrier so each surface picks its own presenter.
type APIError struct{ Problem Problem }

// Error renders the problem as a single line "<code>: <detail>" so it
// flows through %w chains and errors.Is unwrapping. Surfaces that want
// the three-line UX §3.3 shape can branch on the field directly and
// construct their own rendering — see cmd/faas/client.go for the CLI
// implementation.
func (e *APIError) Error() string {
	p := e.Problem
	if p.Detail != "" {
		return fmt.Sprintf("%s: %s", p.Code, p.Detail)
	}
	return p.Code
}

// Unwrap returns the SDK's sentinel error for the canonical Problem
// codes (not_found, unauthorized, rate_limited, capacity_unavailable).
// Callers can use errors.Is to test for these without importing
// internal/api:
//
//	if errors.Is(err, faas.ErrNotFound) { ... }
//
// Unknown codes (the majority) return nil so the chain stops at
// APIError — callers fall through to errors.As(&api.APIError{}) for
// the typed wire shape.
func (e *APIError) Unwrap() error {
	switch e.Problem.Code {
	case CodeNotFound:
		return ErrSentinelNotFound
	case CodeUnauthorized:
		return ErrSentinelUnauthorized
	case CodeRateLimited:
		return ErrSentinelRateLimited
	case CodeCapacity, "compute_region_admission_paused", "compute_region_unavailable", "compute_region_capacity_exhausted":
		return ErrSentinelCapacity
	}
	return nil
}

// Sentinel errors. Each maps to one or more server-side Problem.Code
// values. The unwrap path (above) is the only consumer; callers
// compare via errors.Is via the public re-exports in the faas
// package. The names are prefixed with "Sentinel" to avoid colliding
// with the existing Err* constructor functions in this package
// (ErrCapacity, ErrPlanLimitApps, etc., which return *Problem).
var (
	ErrSentinelNotFound     = errors.New("faas: resource not found")
	ErrSentinelUnauthorized = errors.New("faas: invalid credentials")
	ErrSentinelRateLimited  = errors.New("faas: rate limited")
	ErrSentinelCapacity     = errors.New("faas: capacity unavailable")
)

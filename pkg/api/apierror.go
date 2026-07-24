package api

import "fmt"

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

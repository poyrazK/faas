package faas

import (
	"errors"

	"github.com/poyrazK/faas/sdk/go/internal/api"
)

// Sentinel errors exported for the public SDK. Each maps to one or
// more server-side Problem.Code values via the *APIError.Unwrap
// implementation in internal/api/apierror.go. Callers compare with
// errors.Is, e.g.:
//
//	if errors.Is(err, faas.ErrNotFound) { ... }
//
// Adding a new sentinel requires two changes: define the var here,
// and add the matching Problem.Code branch in the Unwrap method.
// PR 12 reconciliation will keep the daemon's pkg/api/apierror.go
// in sync (the daemon's copy will gain the same Unwrap + sentinels
// so cmd/faas benefits too).
var (
	ErrNotFound     = api.ErrSentinelNotFound
	ErrUnauthorized = api.ErrSentinelUnauthorized
	ErrRateLimited  = api.ErrSentinelRateLimited
	ErrCapacity     = api.ErrSentinelCapacity
)

// AsAPIError extracts the *APIError from an error chain, returning
// (apiErr, true) on success. This is a thin convenience over
// errors.As(&apiErr) for callers that want a single line:
//
//	if ae, ok := faas.AsAPIError(err); ok {
//	    log.Printf("api: %s (%s)", ae.Code, ae.Detail)
//	}
//
// Use errors.As directly when the target type is not *faas.APIError.
func AsAPIError(err error) (*APIError, bool) {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}

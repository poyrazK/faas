// Package pgerrors defines the shared managed PostgreSQL error identities.
// Catalogue and state protocols can classify failures without depending on
// the managed database service and its lifecycle implementation.
package pgerrors

import "errors"

var (
	ErrUnavailable   = errors.New("managed postgres unavailable")
	ErrNotFound      = errors.New("managed postgres resource not found")
	ErrConflict      = errors.New("managed postgres resource conflict")
	ErrInvalid       = errors.New("invalid managed postgres request")
	ErrUnsupported   = errors.New("managed postgres feature unsupported")
	ErrQuotaExceeded = errors.New("managed postgres quota exceeded")
	ErrUsageStale    = errors.New("managed postgres usage is stale")
)

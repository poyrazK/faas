package managedpostgres

import (
	"errors"
	"fmt"
	"strings"
)

// OperatorReason is an adapter-authored explanation of a provider error that
// is safe to log: fixed step names and provider identifiers, never response
// payloads or credentials. Raw provider errors stay unlogged; an adapter opts
// a reason in by constructing it. Without these, a restore stuck in
// provisioning left only a bare sentinel in apid's log (production-us live
// test 2026-10-14).
type OperatorReason struct {
	Err    error
	Reason string
}

func (r *OperatorReason) Error() string { return r.Err.Error() + ": " + r.Reason }
func (r *OperatorReason) Unwrap() error { return r.Err }

// Reasonf wraps err, usually a sentinel, with a loggable reason.
func Reasonf(err error, format string, args ...any) error {
	return &OperatorReason{Err: err, Reason: fmt.Sprintf(format, args...)}
}

// LoggableReason joins the operator reasons in err's chain, outermost first.
// It never includes the text of the errors those reasons wrap.
func LoggableReason(err error) string {
	var normalized *reasonedError
	if errors.As(err, &normalized) {
		return normalized.reason
	}
	var reasons []string
	for {
		var reason *OperatorReason
		if !errors.As(err, &reason) {
			return strings.Join(reasons, ": ")
		}
		reasons = append(reasons, reason.Reason)
		err = reason.Err
	}
}

// reasonedError keeps an adapter's loggable reason on a normalized error
// while its text stays the bare sentinel that callers and API problems show.
type reasonedError struct {
	err    error
	reason string
}

func (e *reasonedError) Error() string { return e.err.Error() }
func (e *reasonedError) Unwrap() error { return e.err }

func withLoggableReason(normalized, providerErr error) error {
	if reason := LoggableReason(providerErr); reason != "" {
		return &reasonedError{err: normalized, reason: reason}
	}
	return normalized
}

package sched

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// Dispatch errors can include invocation headers or event payloads. Keep
// operational logs useful without serializing any request-derived error text.
func dispatchErrorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, state.ErrNotFound):
		return "not_found"
	case errors.Is(err, state.ErrConflict):
		return "conflict"
	case errors.Is(err, state.ErrInvalidArgument):
		return "invalid_argument"
	default:
		return "internal"
	}
}

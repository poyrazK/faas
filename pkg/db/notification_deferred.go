package db

import (
	"time"
)

// DeferredNotificationError represents expected dependency waiting. It keeps
// durable work pending without spending the delivery failure budget.
type DeferredNotificationError struct {
	Cause error
	Delay time.Duration
}

func (e *DeferredNotificationError) Error() string { return e.Cause.Error() }
func (e *DeferredNotificationError) Unwrap() error { return e.Cause }

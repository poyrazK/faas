package state

import (
	"context"
	"time"
)

// WorkflowAlertSnapshot uses terminal time for windowed counts and current
// durable state for ages. Pending age starts at eligibility and excludes future
// retries. Waiting age includes intentional timers and callbacks.
type WorkflowAlertSnapshot struct {
	Failures          int64
	QuotaSkips        int64
	PendingAgeSeconds float64
	WaitingAgeSeconds float64
}

type WorkflowAlertStore interface {
	WorkflowAlertSnapshot(context.Context, string, string, time.Time, time.Time) (WorkflowAlertSnapshot, error)
}

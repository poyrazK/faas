package state

import (
	"context"
	"time"
)

// WorkflowAlertSnapshot uses terminal time for windowed counts and current
// durable state for ages. Pending age starts at eligibility and excludes future
// retries. Waiting age includes intentional timers and callbacks.
// Due age includes pending runs, elapsed parked wakes and expired running
// leases, using the same eligibility clock as automation queue health.
type WorkflowAlertSnapshot struct {
	Failures          int64
	QuotaSkips        int64
	PendingAgeSeconds float64
	WaitingAgeSeconds float64
	DueAgeSeconds     float64
}

type WorkflowAlertStore interface {
	WorkflowAlertSnapshot(context.Context, string, string, time.Time, time.Time) (WorkflowAlertSnapshot, error)
}

package state

import (
	"context"
	"time"
)

// JobBillingInstance retains ownership for internal billing even when the job
// is soft deleted and therefore hidden by customer-facing job lookups.
type JobBillingInstance struct {
	Instance
	AccountID string
}

// JobBillingWindowStore resolves billing members from residency rather than
// current lifecycle state or customer-visible job status.
type JobBillingWindowStore interface {
	ListJobInstancesInBillingWindow(context.Context, time.Time, time.Time) ([]JobBillingInstance, error)
}

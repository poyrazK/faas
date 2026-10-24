package state

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// BillingDeliveryReader reads the pusher's compute delivery receipts for the
// ADR-20261019193600 ledger dual run. It never writes.
type BillingDeliveryReader interface {
	// DeliveredComputeMBSeconds sums the compute MB-seconds recorded as
	// delivered to provider for accountID in hour windows starting in
	// [start, end).
	DeliveredComputeMBSeconds(ctx context.Context, provider, accountID string, start, end time.Time) (int64, error)
}

func validDeliveredComputeRange(provider, accountID string, start, end time.Time) bool {
	return provider != "" && uuid.Validate(accountID) == nil && !start.IsZero() && start.Before(end)
}

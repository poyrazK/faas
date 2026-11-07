package state

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// BillingDeliveryReader reads the pusher's delivery receipts for the
// ADR-20261019193600 ledger dual run. It never writes.
type BillingDeliveryReader interface {
	// DeliveredComputeMBSeconds sums the compute MB-seconds recorded as
	// delivered to provider for accountID in hour windows starting in
	// [start, end).
	DeliveredComputeMBSeconds(ctx context.Context, provider, accountID string, start, end time.Time) (int64, error)
	// DeliveredEgressBytes sums the egress bytes the pusher settled for
	// provider and accountID in hour windows starting in [start, end), in
	// shadow or live delivery.
	DeliveredEgressBytes(ctx context.Context, provider, accountID string, start, end time.Time) (int64, error)
}

// HourQuantity is one meter's quantity for one UTC hour.
type HourQuantity struct {
	Hour     time.Time
	Quantity int64
}

// FinancialHourlyUsageReader reads ledger evidence hour by hour, so the dual
// run can apply billing rules that run per hour (the egress allowance in
// force, the overage switch, Free hours) to the ledger itself
// (ADR-20261019193600).
type FinancialHourlyUsageReader interface {
	// FinancialHourlyUsage sums account's evidence for meter by the UTC hour
	// its source started in, for sources inside [start, end) with an id at or
	// below through. Hours without evidence are absent; the result is in
	// hour order.
	FinancialHourlyUsage(ctx context.Context, account, meter string, start, end time.Time, through int64) ([]HourQuantity, error)
}

// HourlyMeterUsageReader reads an account's usage_minutes hour by hour, the
// source the pusher bills from, so a month-to-date figure that follows the
// pusher's hour rules (the egress the spend cap reserves) can be computed.
type HourlyMeterUsageReader interface {
	// HourlyMeterUsage sums accountID's usage of meter (compute MB-seconds or
	// interface egress bytes) by UTC hour for minutes in [start, end). Hours
	// without usage are absent; the result is in hour order.
	HourlyMeterUsage(ctx context.Context, accountID string, meter BillingMeter, start, end time.Time) ([]HourQuantity, error)
}

func validDeliveredComputeRange(provider, accountID string, start, end time.Time) bool {
	return provider != "" && uuid.Validate(accountID) == nil && !start.IsZero() && start.Before(end)
}

func validHourlyMeterUsage(accountID string, meter BillingMeter, start, end time.Time) bool {
	return accountID != "" && (meter == BillingMeterCompute || meter == BillingMeterEgress) && !start.IsZero() && start.Before(end)
}

package state

import (
	"context"
	"time"
)

var _ BillingDeliveryReader = (*MemStore)(nil)

func (m *MemStore) DeliveredComputeMBSeconds(_ context.Context, provider, accountID string, start, end time.Time) (int64, error) {
	if !validDeliveredComputeRange(provider, accountID, start, end) {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var total int64
	for _, d := range m.billingUsageDeliveries {
		if d.provider == provider && d.accountID == accountID && d.meter == BillingMeterCompute && !d.hour.Before(start.UTC()) && d.hour.Before(end.UTC()) {
			total += d.quantity
		}
	}
	return total, nil
}

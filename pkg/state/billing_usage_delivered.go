package state

import (
	"context"
	"time"
)

// BillingUsageDeliveredSummer sums the compute usage meterd delivered to a
// provider by the time it was delivered. A provider that stamps each usage
// event when it is sent (Polar, ADR-20261028101500) buckets usage the same
// way, so the billing reconciler compares the two (bug hunt 2026-10-28 B22).
type BillingUsageDeliveredSummer interface {
	SumBillingUsageDelivered(ctx context.Context, provider, accountID string, start, end time.Time) (int64, error)
}

// SumBillingUsageDelivered totals billing_usage_deliveries delivered in
// [start, end).
func (s *PgStore) SumBillingUsageDelivered(ctx context.Context, provider, accountID string, start, end time.Time) (int64, error) {
	var total int64
	err := s.pool.QueryRow(ctx,
		`select coalesce(sum(mb_seconds), 0)::bigint from billing_usage_deliveries
		 where provider = $1 and account_id = $2 and delivered_at >= $3 and delivered_at < $4`,
		provider, accountID, start.UTC(), end.UTC()).Scan(&total)
	return total, err
}

// SumBillingUsageDelivered mirrors PgStore.SumBillingUsageDelivered.
func (m *MemStore) SumBillingUsageDelivered(_ context.Context, provider, accountID string, start, end time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var total int64
	for _, d := range m.billingUsageDeliveries {
		if d.provider == provider && d.accountID == accountID && d.meter == BillingMeterCompute &&
			!d.deliveredAt.Before(start) && d.deliveredAt.Before(end) {
			total += d.quantity
		}
	}
	return total, nil
}

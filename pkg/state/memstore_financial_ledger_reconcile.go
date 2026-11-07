package state

import (
	"context"
	"math"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/financial"
)

var (
	_ BillingDeliveryReader      = (*MemStore)(nil)
	_ FinancialHourlyUsageReader = (*MemStore)(nil)
	_ HourlyMeterUsageReader     = (*MemStore)(nil)
)

func (m *MemStore) DeliveredComputeMBSeconds(_ context.Context, provider, accountID string, start, end time.Time) (int64, error) {
	return m.deliveredMeterQuantity(provider, accountID, BillingMeterCompute, start, end)
}

func (m *MemStore) DeliveredEgressBytes(_ context.Context, provider, accountID string, start, end time.Time) (int64, error) {
	return m.deliveredMeterQuantity(provider, accountID, BillingMeterEgress, start, end)
}

func (m *MemStore) deliveredMeterQuantity(provider, accountID string, meter BillingMeter, start, end time.Time) (int64, error) {
	if !validDeliveredComputeRange(provider, accountID, start, end) {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var total int64
	for _, d := range m.billingUsageDeliveries {
		if d.provider == provider && d.accountID == accountID && d.meter == meter && !d.hour.Before(start.UTC()) && d.hour.Before(end.UTC()) {
			total += d.quantity
		}
	}
	return total, nil
}

func (m *MemStore) FinancialHourlyUsage(_ context.Context, account, meter string, start, end time.Time, through int64) ([]HourQuantity, error) {
	if account == "" || meter == "" || through < 0 || !validFinancialSamplingWindow(start, end) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	byHour := map[time.Time]int64{}
	for _, row := range m.financialEvidence {
		e := row.Evidence
		if e.AccountID != account || e.Meter != meter || row.Sequence > through || e.Start.Before(start) || e.End.After(end) {
			continue
		}
		if err := addHourQuantity(byHour, e.Start, e.Quantity); err != nil {
			return nil, err
		}
	}
	return sortedHourQuantities(byHour), nil
}

func (m *MemStore) HourlyMeterUsage(_ context.Context, accountID string, meter BillingMeter, start, end time.Time) ([]HourQuantity, error) {
	if !validHourlyMeterUsage(accountID, meter, start, end) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	byHour := map[time.Time]int64{}
	for _, row := range m.usage {
		if row.AccountID != accountID || row.Minute.Before(start) || !row.Minute.Before(end) {
			continue
		}
		quantity := row.MBSeconds
		if meter == BillingMeterEgress {
			quantity = row.NetTxBytes
		}
		if err := addHourQuantity(byHour, row.Minute, quantity); err != nil {
			return nil, err
		}
	}
	return sortedHourQuantities(byHour), nil
}

func addHourQuantity(byHour map[time.Time]int64, at time.Time, quantity int64) error {
	hour := at.UTC().Truncate(time.Hour)
	if quantity > 0 && byHour[hour] > math.MaxInt64-quantity || quantity < 0 && byHour[hour] < math.MinInt64-quantity {
		return financial.ErrOverflow
	}
	byHour[hour] += quantity
	return nil
}

func sortedHourQuantities(byHour map[time.Time]int64) []HourQuantity {
	out := make([]HourQuantity, 0, len(byHour))
	for hour, quantity := range byHour {
		if quantity != 0 {
			out = append(out, HourQuantity{Hour: hour, Quantity: quantity})
		}
	}
	slices.SortFunc(out, func(a, b HourQuantity) int { return a.Hour.Compare(b.Hour) })
	return out
}

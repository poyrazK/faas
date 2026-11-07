package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	_ BillingDeliveryReader      = (*PgStore)(nil)
	_ FinancialHourlyUsageReader = (*PgStore)(nil)
	_ HourlyMeterUsageReader     = (*PgStore)(nil)
)

func (s *PgStore) DeliveredComputeMBSeconds(ctx context.Context, provider, accountID string, start, end time.Time) (int64, error) {
	if !validDeliveredComputeRange(provider, accountID, start, end) {
		return 0, ErrInvalidArgument
	}
	id, _ := parsePgUUID(accountID)
	total, err := sqlc.New().FinancialLedgerDeliveredComputeMBSeconds(ctx, s.pool, sqlc.FinancialLedgerDeliveredComputeMBSecondsParams{
		Provider:  provider,
		AccountID: id,
		StartAt:   pgtype.Timestamptz{Time: start.UTC(), Valid: true},
		EndAt:     pgtype.Timestamptz{Time: end.UTC(), Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("delivered compute mb-seconds: %w", err)
	}
	return total, nil
}

func (s *PgStore) DeliveredEgressBytes(ctx context.Context, provider, accountID string, start, end time.Time) (int64, error) {
	if !validDeliveredComputeRange(provider, accountID, start, end) {
		return 0, ErrInvalidArgument
	}
	id, _ := parsePgUUID(accountID)
	total, err := sqlc.New().FinancialLedgerDeliveredEgressBytes(ctx, s.pool, sqlc.FinancialLedgerDeliveredEgressBytesParams{
		Provider:  provider,
		AccountID: id,
		StartAt:   pgtype.Timestamptz{Time: start.UTC(), Valid: true},
		EndAt:     pgtype.Timestamptz{Time: end.UTC(), Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("delivered egress bytes: %w", err)
	}
	return total, nil
}

func (s *PgStore) FinancialHourlyUsage(ctx context.Context, account, meter string, start, end time.Time, through int64) ([]HourQuantity, error) {
	if account == "" || meter == "" || through < 0 || !validFinancialSamplingWindow(start, end) {
		return nil, ErrInvalidArgument
	}
	id, err := parsePgUUID(account)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().FinancialUsageHourly(ctx, s.pool, sqlc.FinancialUsageHourlyParams{AccountID: id, Meter: meter,
		PeriodStart: financialTimestamp(start), PeriodEnd: financialTimestamp(end), ThroughID: through})
	if err != nil {
		return nil, fmt.Errorf("financial hourly usage: %w", err)
	}
	out := make([]HourQuantity, 0, len(rows))
	for _, row := range rows {
		if row.Quantity != 0 {
			out = append(out, HourQuantity{Hour: row.Hour.Time.UTC(), Quantity: row.Quantity})
		}
	}
	return out, nil
}

func (s *PgStore) HourlyMeterUsage(ctx context.Context, accountID string, meter BillingMeter, start, end time.Time) ([]HourQuantity, error) {
	if !validHourlyMeterUsage(accountID, meter, start, end) {
		return nil, ErrInvalidArgument
	}
	id, err := parsePgUUID(accountID)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().UsageMinutesHourly(ctx, s.pool, sqlc.UsageMinutesHourlyParams{AccountID: id,
		StartAt: pgtype.Timestamptz{Time: start.UTC(), Valid: true}, EndAt: pgtype.Timestamptz{Time: end.UTC(), Valid: true}})
	if err != nil {
		return nil, fmt.Errorf("hourly meter usage: %w", err)
	}
	out := make([]HourQuantity, 0, len(rows))
	for _, row := range rows {
		quantity := row.MbSeconds
		if meter == BillingMeterEgress {
			quantity = row.NetTxBytes
		}
		if quantity != 0 {
			out = append(out, HourQuantity{Hour: row.Hour.Time.UTC(), Quantity: quantity})
		}
	}
	return out, nil
}

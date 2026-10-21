package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ BillingDeliveryReader = (*PgStore)(nil)

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

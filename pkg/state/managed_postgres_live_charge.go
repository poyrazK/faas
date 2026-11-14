package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ManagedPostgresLiveChargeStore reads the live managed PostgreSQL overage
// charge a subject's spend cap counts (billing review 2026-11-13, R7-1:
// ADR-20261031110037 decision 7 amended). Delivered is what the provider was
// sent for the UTC month of periodStart; pending is reserved in a step apid
// has not finished. A store without managed PostgreSQL charges reports none.
type ManagedPostgresLiveChargeStore interface {
	LiveManagedPostgresMillicents(ctx context.Context, subjectID string, periodStart time.Time) (delivered, pending int64, err error)
}

// LiveManagedPostgresMillicents implements ManagedPostgresLiveChargeStore.
func (s *PgStore) LiveManagedPostgresMillicents(ctx context.Context, subjectID string, periodStart time.Time) (int64, int64, error) {
	id, err := parsePgUUID(subjectID)
	if err != nil {
		return 0, 0, err
	}
	at := periodStart.UTC()
	month := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	row, err := sqlc.New().ManagedPostgresLiveOverageMillicents(ctx, s.pool, sqlc.ManagedPostgresLiveOverageMillicentsParams{
		SubjectID: id, PeriodStart: pgtype.Timestamptz{Time: month, Valid: true}})
	if err != nil {
		return 0, 0, fmt.Errorf("state: read live managed postgres charge: %w", mapErr(err))
	}
	return row.DeliveredMillicents, row.PendingMillicents, nil
}

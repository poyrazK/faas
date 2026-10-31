package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// GetBillingProgress implements BillingProgressStore (ADR-20261031165104).
func (s *PostgresStore) GetBillingProgress(ctx context.Context, provider, subjectID string, periodStart time.Time, mode string) (BillingProgress, error) {
	subject, err := postgresUUID(subjectID)
	if err != nil {
		return BillingProgress{}, ErrInvalid
	}
	row, err := sqlc.New().GetManagedPostgresBillingProgress(ctx, s.catalog, sqlc.GetManagedPostgresBillingProgressParams{
		Provider: provider, SubjectID: subject, PeriodStart: databaseNullableTime(monthStart(periodStart)), Mode: mode})
	if err != nil {
		return BillingProgress{}, mapPostgresError(err)
	}
	return billingProgressFromSQL(row), nil
}

// SaveBillingProgress implements BillingProgressStore: version zero inserts,
// any other version updates only a row still at it.
func (s *PostgresStore) SaveBillingProgress(ctx context.Context, p BillingProgress, now time.Time) (BillingProgress, error) {
	p.PeriodStart = monthStart(p.PeriodStart)
	if !p.valid() {
		return BillingProgress{}, ErrInvalid
	}
	subject, err := postgresUUID(p.SubjectID)
	if err != nil {
		return BillingProgress{}, ErrInvalid
	}
	closedAt := pgtype.Timestamptz{}
	if p.ClosedAt != nil {
		closedAt = databaseNullableTime(*p.ClosedAt)
	}
	q := sqlc.New()
	var row sqlc.ManagedPostgresBillingProgress
	if p.Version == 0 {
		row, err = q.InsertManagedPostgresBillingProgress(ctx, s.catalog, sqlc.InsertManagedPostgresBillingProgressParams{
			Provider: p.Provider, SubjectID: subject, PeriodStart: databaseNullableTime(p.PeriodStart), Mode: p.Mode,
			DeliveredMillicents: p.DeliveredMillicents, PendingMillicents: p.PendingMillicents, PendingFixed: p.PendingFixed,
			ClosedAt: closedAt, UpdatedAt: databaseNullableTime(now)})
	} else {
		row, err = q.UpdateManagedPostgresBillingProgress(ctx, s.catalog, sqlc.UpdateManagedPostgresBillingProgressParams{
			DeliveredMillicents: p.DeliveredMillicents, PendingMillicents: p.PendingMillicents, PendingFixed: p.PendingFixed,
			ClosedAt: closedAt, UpdatedAt: databaseNullableTime(now), Provider: p.Provider, SubjectID: subject,
			PeriodStart: databaseNullableTime(p.PeriodStart), Mode: p.Mode, ExpectedVersion: p.Version})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// The insert met an existing row, or the update a newer version.
		return BillingProgress{}, ErrConflict
	}
	if err != nil {
		return BillingProgress{}, mapPostgresError(err)
	}
	return billingProgressFromSQL(row), nil
}

func billingProgressFromSQL(row sqlc.ManagedPostgresBillingProgress) BillingProgress {
	p := BillingProgress{Provider: row.Provider, SubjectID: databaseUUID(row.SubjectID), PeriodStart: row.PeriodStart.Time.UTC(),
		Mode: row.Mode, DeliveredMillicents: row.DeliveredMillicents, PendingMillicents: row.PendingMillicents,
		PendingFixed: row.PendingFixed, Version: row.Version, UpdatedAt: row.UpdatedAt.Time.UTC()}
	if row.ClosedAt.Valid {
		at := row.ClosedAt.Time.UTC()
		p.ClosedAt = &at
	}
	return p
}

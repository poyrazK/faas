package managedpostgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ AccountingDiagnosticsStore = (*PostgresStore)(nil)

func (s *PostgresStore) ListAccountingCoverage(ctx context.Context, accountID, afterID string, limit int) ([]AccountingCoverage, error) {
	if limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	account, err := postgresUUID(accountID)
	if err != nil {
		return nil, err
	}
	params := sqlc.ListManagedPostgresAccountingCoverageParams{AccountID: account, PageLimit: pgtype.Int4{Int32: int32(limit), Valid: true}}
	if afterID != "" {
		params.AfterID, err = postgresUUID(afterID)
		if err != nil {
			return nil, err
		}
	}
	rows, err := sqlc.New().ListManagedPostgresAccountingCoverage(ctx, s.pool, params)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	items := make([]AccountingCoverage, 0, len(rows))
	for _, row := range rows {
		items = append(items, accountingCoverageFromRow(row))
	}
	return items, nil
}

func accountingCoverageFromRow(row sqlc.ListManagedPostgresAccountingCoverageRow) AccountingCoverage {
	progress := usageProgressFromColumns(time.Duration(row.WindowSeconds)*time.Second,
		row.CollectedFrom, row.CollectedUntil, row.ObservedAt,
		pgtype.Text{String: row.SourceDatabaseID, Valid: row.SourceDatabaseID != ""})
	progress.CorrectionObservedAt = row.CorrectionObservedAt.Time
	progress.Terminal = row.AccountingState == string(StateDeleted)
	progress.Unresolved = row.Unresolved
	progress.EndedAt = row.EndedAt.Time
	return AccountingCoverage{DatabaseID: cutoverUUID(row.DatabaseID), Name: row.Name, State: State(row.State),
		AccountingRequired: row.AccountingRequired, IdentityKnown: row.IdentityKnown,
		AccountingDatabaseID: cutoverUUID(row.AccountingDatabaseID), CreatedAt: row.AccountingCreatedAt.Time,
		LeaseUntil: row.LeaseUntil.Time, Progress: progress}
}

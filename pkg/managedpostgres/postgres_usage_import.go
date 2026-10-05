package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PostgresStore) ImportUsage(ctx context.Context, command UsageImportCommand, apply bool) (UsageImportResult, error) {
	if err := validateUsageImportRequest(command.Request, command.ActorID, command.Now, apply); err != nil {
		return UsageImportResult{}, err
	}
	if len(command.Records) == 0 || len(command.Records[0]) == 0 {
		return UsageImportResult{}, ErrInvalid
	}
	account, err := postgresUUID(command.Expected.AccountID)
	if err != nil {
		return UsageImportResult{}, err
	}
	id, err := postgresUUID(command.Expected.ID)
	if err != nil {
		return UsageImportResult{}, err
	}
	importID, err := postgresUUID(command.Request.ImportID)
	if err != nil {
		return UsageImportResult{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return UsageImportResult{}, mapPostgresError(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	queries := sqlc.New()
	row, err := queries.LockManagedPostgresUsageResource(ctx, tx, id)
	if err != nil {
		return UsageImportResult{}, mapPostgresError(err)
	}
	fingerprint := usageImportRequestHash(command.ActorID, command.Request)
	if apply {
		receipt, err := queries.GetManagedPostgresUsageImport(ctx, tx, sqlc.GetManagedPostgresUsageImportParams{AccountID: account, ImportID: importID})
		if err == nil {
			if receipt.RequestSha256 != fingerprint {
				return UsageImportResult{}, ErrConflict
			}
			var result UsageImportResult
			if err := json.Unmarshal(receipt.Result, &result); err != nil {
				return UsageImportResult{}, err
			}
			return result, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return UsageImportResult{}, mapPostgresError(err)
		}
	}
	window := command.Records[0][0].WindowTo.Sub(command.Records[0][0].WindowFrom)
	progress, err := postgresImportProgress(ctx, tx, id, window)
	if err != nil {
		return UsageImportResult{}, err
	}
	windows := command.Request.Windows
	rows, err := queries.ListManagedPostgresImportRecords(ctx, tx, sqlc.ListManagedPostgresImportRecordsParams{
		DatabaseID: id, WindowFrom: importTimestamp(windows[0].From), WindowTo: importTimestamp(windows[len(windows)-1].To)})
	if err != nil {
		return UsageImportResult{}, mapPostgresError(err)
	}
	var existing []UsageRecord
	for _, r := range rows {
		existing = append(existing, UsageRecord{AccountID: cutoverUUID(r.AccountID), DatabaseID: cutoverUUID(r.DatabaseID),
			BackendID: r.BackendID, BackendFingerprint: r.BackendFingerprint, WindowFrom: r.WindowFrom.Time.UTC(),
			WindowTo: r.WindowTo.Time.UTC(), ObservedAt: r.ObservedAt.Time.UTC(), Meter: Meter(r.Meter), Quantity: r.Quantity, CostMillicents: r.CostMillicents})
	}
	plan, err := planUsageImport(command, databaseFromRow(row), progress, existing)
	if err != nil {
		return UsageImportResult{}, err
	}
	if !apply {
		return plan.Result, nil
	}
	if plan.Result.Revision != command.Request.ExpectedRevision {
		return UsageImportResult{}, ErrConflict
	}
	if err := postgresWriteImportUsage(ctx, tx, id, account, window, plan); err != nil {
		return UsageImportResult{}, err
	}
	plan.Result.Applied = true
	request, _ := json.Marshal(command.Request)
	policy, _ := json.Marshal(command.Policy)
	before, _ := json.Marshal(plan.Before)
	after, _ := json.Marshal(plan.After)
	result, _ := json.Marshal(plan.Result)
	err = queries.InsertManagedPostgresUsageImport(ctx, tx, sqlc.InsertManagedPostgresUsageImportParams{
		AccountID: account, ImportID: importID, DatabaseID: id, ActorID: command.ActorID, Reason: command.Request.Reason,
		EvidenceReference: command.Request.EvidenceReference, EvidenceSha256: command.Request.EvidenceSHA256,
		RequestSha256: fingerprint, PreviewRevision: command.Request.ExpectedRevision, Request: request, Policy: policy,
		BeforeRecords: before, AfterRecords: after, Result: result, CreatedAt: importTimestamp(command.Now)})
	if err != nil {
		return UsageImportResult{}, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return UsageImportResult{}, mapPostgresError(err)
	}
	return plan.Result, nil
}

func postgresImportProgress(ctx context.Context, tx pgx.Tx, database pgtype.UUID, window time.Duration) (UsageProgress, error) {
	queries := sqlc.New()
	incompatible, err := queries.HasManagedPostgresIncompatibleUsageWindow(ctx, tx, sqlc.HasManagedPostgresIncompatibleUsageWindowParams{DatabaseID: database, WindowSeconds: int64(window / time.Second)})
	if err != nil {
		return UsageProgress{}, mapPostgresError(err)
	}
	if incompatible {
		return UsageProgress{}, ErrConflict
	}
	row, err := queries.GetManagedPostgresRawUsageCoverage(ctx, tx, sqlc.GetManagedPostgresRawUsageCoverageParams{DatabaseID: database, WindowSeconds: int64(window / time.Second)})
	if errors.Is(err, pgx.ErrNoRows) {
		return UsageProgress{}, nil
	}
	if err != nil {
		return UsageProgress{}, mapPostgresError(err)
	}
	source := pgtype.Text{String: cutoverUUID(row.SourceDatabaseID), Valid: row.SourceDatabaseID.Valid}
	return usageProgressFromColumns(window, row.CollectedFrom, row.CollectedUntil, row.ObservedAt, source), nil
}

func postgresWriteImportUsage(ctx context.Context, tx pgx.Tx, database, account pgtype.UUID, window time.Duration, plan usageImportPlan) error {
	queries := sqlc.New()
	for _, r := range plan.After {
		err := queries.UpsertManagedPostgresUsageRecord(ctx, tx, sqlc.UpsertManagedPostgresUsageRecordParams{
			AccountID: account, DatabaseID: database, BackendID: r.BackendID, BackendFingerprint: r.BackendFingerprint,
			WindowFrom: importTimestamp(r.WindowFrom), WindowTo: importTimestamp(r.WindowTo), ObservedAt: importTimestamp(r.ObservedAt),
			Meter: string(r.Meter), Quantity: r.Quantity, CostMillicents: r.CostMillicents})
		if err != nil {
			return mapPostgresError(err)
		}
	}
	err := queries.UpsertManagedPostgresUsageCoverage(ctx, tx, sqlc.UpsertManagedPostgresUsageCoverageParams{DatabaseID: database,
		WindowSeconds: int64(window / time.Second), CollectedFrom: importTimestamp(plan.Progress.CollectedFrom),
		CollectedUntil: importTimestamp(plan.Progress.CollectedUntil), ObservedAt: importTimestamp(plan.Progress.ObservedAt)})
	return mapPostgresError(err)
}

func importTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func (s *PostgresStore) ReplayUsageImport(ctx context.Context, accountID, actor string, request UsageImportRequest) (UsageImportResult, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return UsageImportResult{}, err
	}
	id, err := postgresUUID(request.ImportID)
	if err != nil {
		return UsageImportResult{}, err
	}
	receipt, err := sqlc.New().GetManagedPostgresUsageImport(ctx, s.pool, sqlc.GetManagedPostgresUsageImportParams{AccountID: account, ImportID: id})
	if err != nil {
		return UsageImportResult{}, mapPostgresError(err)
	}
	if receipt.RequestSha256 != usageImportRequestHash(actor, request) {
		return UsageImportResult{}, ErrConflict
	}
	var result UsageImportResult
	if err := json.Unmarshal(receipt.Result, &result); err != nil {
		return UsageImportResult{}, err
	}
	return result, nil
}

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

func (s *PostgresStore) ReconcileAccounting(ctx context.Context, command AccountingReconciliationCommand, apply bool) (AccountingReconciliationResult, error) {
	if err := validateAccountingReconciliation(command.Request, command.ActorID, command.Now, apply); err != nil {
		return AccountingReconciliationResult{}, err
	}
	account, err := postgresUUID(command.Expected.AccountID)
	if err != nil {
		return AccountingReconciliationResult{}, err
	}
	id, err := postgresUUID(command.Expected.ID)
	if err != nil {
		return AccountingReconciliationResult{}, err
	}
	reconciliationID, err := postgresUUID(command.Request.ReconciliationID)
	if err != nil {
		return AccountingReconciliationResult{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AccountingReconciliationResult{}, mapPostgresError(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	queries := sqlc.New()
	row, err := queries.LockManagedPostgresUsageResource(ctx, tx, id)
	if err != nil {
		return AccountingReconciliationResult{}, mapPostgresError(err)
	}
	fingerprint := accountingReconciliationHash(command.ActorID, command.Request)
	if apply {
		receipt, err := queries.GetManagedPostgresAccountingReconciliation(ctx, tx, sqlc.GetManagedPostgresAccountingReconciliationParams{AccountID: account, ReconciliationID: reconciliationID})
		if err == nil {
			return decodeAccountingReconciliation(receipt.RequestSha256, receipt.Result, fingerprint)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return AccountingReconciliationResult{}, mapPostgresError(err)
		}
		// Claims for different catalog rows serialize before their conflict read.
		scope := command.Request.BackendID + "\x1f" + command.Request.BackendFingerprint + "\x1f" + command.Request.ProviderResourceID
		if err := queries.LockManagedPostgresReconciliationIdentity(ctx, tx, scope); err != nil {
			return AccountingReconciliationResult{}, mapPostgresError(err)
		}
	}
	claimed, err := queries.HasManagedPostgresReconciliationIdentity(ctx, tx, sqlc.HasManagedPostgresReconciliationIdentityParams{
		BackendID: command.Request.BackendID, BackendFingerprint: command.Request.BackendFingerprint, ProviderResourceID: command.Request.ProviderResourceID, DatabaseID: id})
	if err != nil {
		return AccountingReconciliationResult{}, mapPostgresError(err)
	}
	coverageRows, err := queries.ListManagedPostgresReconciliationCoverage(ctx, tx, id)
	if err != nil {
		return AccountingReconciliationResult{}, mapPostgresError(err)
	}
	var coverage []reconciliationCoverage
	for _, p := range coverageRows {
		sourceID := ""
		if p.SourceDatabaseID.Valid {
			sourceID = cutoverUUID(p.SourceDatabaseID)
		}
		coverage = append(coverage, reconciliationCoverage{p.WindowSeconds, p.CollectedFrom.Time.UTC(), p.CollectedUntil.Time.UTC(), p.ObservedAt.Time.UTC(), p.UpdatedAt.Time.UTC(), sourceID})
	}
	meters := make([]string, len(command.Meters))
	for i, m := range command.Meters {
		meters[i] = string(m)
	}
	bounds, err := queries.GetManagedPostgresReconciliationLedger(ctx, tx, sqlc.GetManagedPostgresReconciliationLedgerParams{
		AccountID: account, BackendID: command.Expected.BackendID, BackendFingerprint: command.Expected.BackendFingerprint, Meters: meters, WindowSeconds: int64(command.Policy.Window / time.Second), DatabaseID: id})
	if err != nil {
		return AccountingReconciliationResult{}, mapPostgresError(err)
	}
	ledger := reconciliationLedger{bounds.Records, bounds.FirstWindow.Time.UTC(), bounds.LastWindow.Time.UTC(), bounds.LastObservation.Time.UTC(), bounds.Invalid}
	plan, err := planAccountingReconciliation(command, usageDatabaseFromRow(row), coverage, ledger, claimed.Bool)
	if err != nil {
		return AccountingReconciliationResult{}, err
	}
	if !apply {
		return plan.Result, nil
	}
	if plan.Result.Revision != command.Request.ExpectedRevision {
		return AccountingReconciliationResult{}, ErrConflict
	}
	count, err := queries.ReconcileManagedPostgresLegacyResource(ctx, tx, sqlc.ReconcileManagedPostgresLegacyResourceParams{
		ProviderResourceID: command.Request.ProviderResourceID, ShutdownAt: importTimestamp(command.Request.ShutdownAt), Now: importTimestamp(command.Now), ID: id, AccountID: account, BackendID: command.Expected.BackendID, BackendFingerprint: command.Expected.BackendFingerprint})
	if err != nil {
		return AccountingReconciliationResult{}, mapPostgresError(err)
	}
	if count != 1 {
		return AccountingReconciliationResult{}, ErrConflict
	}
	if err := queries.ResetManagedPostgresReconciliationCoverage(ctx, tx, id); err != nil {
		return AccountingReconciliationResult{}, mapPostgresError(err)
	}
	plan.Result.Applied = true
	if err := postgresRecordAccountingReconciliation(ctx, tx, command, plan, account, id, reconciliationID, fingerprint); err != nil {
		return AccountingReconciliationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AccountingReconciliationResult{}, mapPostgresError(err)
	}
	return plan.Result, nil
}

func postgresRecordAccountingReconciliation(ctx context.Context, tx pgx.Tx, command AccountingReconciliationCommand, plan accountingReconciliationPlan, account, database, reconciliation pgtype.UUID, fingerprint string) error {
	request, _ := json.Marshal(command.Request)
	policy, _ := json.Marshal(command.Policy)
	before, _ := json.Marshal(plan.Before)
	after, _ := json.Marshal(plan.After)
	coverage, _ := json.Marshal(plan.Coverage)
	result, _ := json.Marshal(plan.Result)
	err := sqlc.New().InsertManagedPostgresAccountingReconciliation(ctx, tx, sqlc.InsertManagedPostgresAccountingReconciliationParams{
		AccountID: account, ReconciliationID: reconciliation, DatabaseID: database, BackendID: command.Request.BackendID,
		BackendFingerprint: command.Request.BackendFingerprint, ProviderResourceID: command.Request.ProviderResourceID, ActorID: command.ActorID,
		Reason: command.Request.Reason, EvidenceReference: command.Request.EvidenceReference, EvidenceSha256: command.Request.EvidenceSHA256,
		RequestSha256: fingerprint, PreviewRevision: command.Request.ExpectedRevision, Request: request, Policy: policy,
		BeforeCatalog: before, AfterCatalog: after, CoverageBefore: coverage, Result: result, CreatedAt: importTimestamp(command.Now)})
	return mapPostgresError(err)
}

func decodeAccountingReconciliation(stored string, data []byte, fingerprint string) (AccountingReconciliationResult, error) {
	if stored != fingerprint {
		return AccountingReconciliationResult{}, ErrConflict
	}
	var result AccountingReconciliationResult
	err := json.Unmarshal(data, &result)
	return result, err
}

func (s *PostgresStore) ReplayAccountingReconciliation(ctx context.Context, accountID, actor string, request AccountingReconciliationRequest) (AccountingReconciliationResult, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return AccountingReconciliationResult{}, err
	}
	id, err := postgresUUID(request.ReconciliationID)
	if err != nil {
		return AccountingReconciliationResult{}, err
	}
	receipt, err := sqlc.New().GetManagedPostgresAccountingReconciliation(ctx, s.pool, sqlc.GetManagedPostgresAccountingReconciliationParams{AccountID: account, ReconciliationID: id})
	if err != nil {
		return AccountingReconciliationResult{}, mapPostgresError(err)
	}
	return decodeAccountingReconciliation(receipt.RequestSha256, receipt.Result, accountingReconciliationHash(actor, request))
}

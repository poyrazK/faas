package ci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func sourceCheckRetryRow(row sqlc.CiSourceCheckRetry) SourceCheckRetry {
	out := SourceCheckRetry{ID: uuidString(row.ID), EventID: uuidString(row.EventID), Generation: row.Generation, RequestedAt: row.RequestedAt.Time}
	if row.AppliedAt.Valid {
		out.AppliedAt = &row.AppliedAt.Time
	}
	return out
}

func (s *PostgresStore) sourceCheckDeliveries(ctx context.Context, account, binding string, event pgtype.UUID) ([]SourceCheckDelivery, error) {
	rows, err := s.q.ListCISourceCheckDeliveries(ctx, s.pool, sqlc.ListCISourceCheckDeliveriesParams{AccountID: dbUUID(account), BindingID: dbUUID(binding), EventID: event, MaxRows: api.CIGitHubMaxTriggersPerAccount})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]SourceCheckDelivery, 0, len(rows))
	for _, row := range rows {
		item := SourceCheckDelivery{EventID: uuidString(row.EventID), BindingID: uuidString(row.BindingID), Pipeline: row.Pipeline, CommitSHA: row.CommitSha, SourceStatus: row.SourceStatus,
			DeliveryStatus: row.DeliveryStatus, Attempts: int(row.Attempts), LastError: row.LastError, CheckID: row.CheckID, RetryGeneration: row.RetryGeneration, MaxRetries: api.CICheckRetryMaxGenerations}
		if row.RetryID.Valid {
			retry := sourceCheckRetryRow(sqlc.CiSourceCheckRetry{ID: row.RetryID, EventID: row.EventID, Generation: row.RequestedGeneration, RequestedAt: row.RequestedAt, AppliedAt: row.AppliedAt})
			item.LastRetry, item.RetryPending = &retry, retry.AppliedAt == nil
		}
		item.Retryable = checkDeliveryExhausted(item.DeliveryStatus, item.LastError, item.Attempts) && !item.RetryPending && item.RetryGeneration < api.CICheckRetryMaxGenerations
		if row.NextAttemptAt.Valid {
			item.NextAttemptAt = &row.NextAttemptAt.Time
		}
		if row.LeaseUntil.Valid {
			item.LeaseExpiresAt = &row.LeaseUntil.Time
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *PostgresStore) SourceCheckDeliveries(ctx context.Context, account, binding string) ([]SourceCheckDelivery, error) {
	if !ValidID(account) || !ValidID(binding) {
		return nil, ErrNotFound
	}
	return s.sourceCheckDeliveries(ctx, account, binding, pgtype.UUID{})
}
func (s *PostgresStore) SourceCheckDelivery(ctx context.Context, account, binding, event string) (SourceCheckDelivery, error) {
	if !ValidID(account) || !ValidID(binding) || !ValidID(event) {
		return SourceCheckDelivery{}, ErrNotFound
	}
	rows, err := s.sourceCheckDeliveries(ctx, account, binding, dbUUID(event))
	if err != nil {
		return SourceCheckDelivery{}, err
	}
	if len(rows) != 1 {
		return SourceCheckDelivery{}, ErrNotFound
	}
	return rows[0], nil
}

func (s *PostgresStore) sourceCheckRecoverySource(ctx context.Context, account, binding, event string) (AutomationEvent, error) {
	source, err := s.sourceCheckRetrySource(ctx, s.pool, account, binding, event)
	if err != nil {
		return AutomationEvent{}, err
	}
	row, err := s.q.GetCISourceCheck(ctx, s.pool, sqlc.GetCISourceCheckParams{AccountID: dbUUID(account), EventID: dbUUID(event)})
	if errors.Is(err, pgx.ErrNoRows) {
		return AutomationEvent{}, ErrCheckRetryConflict
	}
	if err != nil {
		return AutomationEvent{}, storeError(err)
	}
	return source, validateSourceCheckProjection(row, source)
}
func (s *PostgresStore) sourceCheckRetrySource(ctx context.Context, db sqlc.DBTX, account, binding, event string) (AutomationEvent, error) {
	row, err := s.q.GetCIAutomationRecoveryEvent(ctx, db, sqlc.GetCIAutomationRecoveryEventParams{AccountID: dbUUID(account), BindingID: dbUUID(binding), ID: dbUUID(event)})
	if err != nil {
		return AutomationEvent{}, storeError(err)
	}
	var decision AutomationPathDecision
	if row.Status != "ignored" || row.LastError != "paths_filtered" || row.ExecutionID.Valid || json.Unmarshal(row.PathDecision, &decision) != nil || decision.Outcome != "skip" || decision.Reason != "paths_filtered" {
		return AutomationEvent{}, ErrNotFound
	}
	source, err := automationEventRow(row)
	if err != nil {
		return AutomationEvent{}, err
	}
	if decision.HeadSHA != source.Source.CommitSHA {
		return AutomationEvent{}, ErrConflict
	}
	return source, nil
}

func validateSourceCheckProjection(row sqlc.CiAutomationPathCheck, source AutomationEvent) error {
	var check ExecutionCheck
	b := source.Binding
	if json.Unmarshal(row.Snapshot, &check) != nil || !check.Valid() || check.CheckID != 0 || check.AccountID != source.AccountID || check.AutomationEventID != source.ID ||
		check.CommitSHA != source.Source.CommitSHA || check.BindingID != b.ID || check.Repository != b.Repository || check.RepositoryID != b.RepositoryID || check.InstallationID != b.InstallationID {
		return ErrConflict
	}
	return nil
}

func (s *PostgresStore) requestSourceCheckRetry(ctx context.Context, account, binding, event, key string, req CheckRetryRequest) (SourceCheckRetry, bool, error) {
	tx, err := s.accountTx(ctx, account)
	if err != nil {
		return SourceCheckRetry{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.q.CIJobsAccount(ctx, tx, dbUUID(account)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SourceCheckRetry{}, false, ErrGitHubAccess
		}
		return SourceCheckRetry{}, false, storeError(err)
	}
	source, err := s.sourceCheckRetrySource(ctx, tx, account, binding, event)
	if err != nil {
		return SourceCheckRetry{}, false, err
	}
	sum := sha256.Sum256([]byte(key))
	digest := hex.EncodeToString(sum[:])
	existing, err := s.q.GetCISourceCheckRetryByKey(ctx, tx, sqlc.GetCISourceCheckRetryByKeyParams{AccountID: dbUUID(account), KeyDigest: digest})
	if err == nil {
		if uuidString(existing.EventID) != event || existing.Generation != req.ExpectedRetryGeneration+1 {
			return SourceCheckRetry{}, false, ErrCheckRetryConflict
		}
		return sourceCheckRetryRow(existing), true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SourceCheckRetry{}, false, storeError(err)
	}
	row, err := s.q.TryLockCISourceCheck(ctx, tx, sqlc.TryLockCISourceCheckParams{AccountID: dbUUID(account), EventID: dbUUID(event)})
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceCheckRetry{}, false, ErrCheckRetryConflict
	}
	if err != nil {
		return SourceCheckRetry{}, false, storeError(err)
	}
	if err := validateSourceCheckProjection(row, source); err != nil {
		return SourceCheckRetry{}, false, err
	}
	if !checkDeliveryExhausted(row.Status, row.LastError, int(row.Attempts)) || row.LeaseToken.Valid || row.LeaseUntil.Valid || row.RetryGeneration != req.ExpectedRetryGeneration {
		return SourceCheckRetry{}, false, ErrCheckRetryConflict
	}
	if row.RetryGeneration >= api.CICheckRetryMaxGenerations {
		return SourceCheckRetry{}, false, &LimitError{Resource: "source_check_retry_generations", Limit: api.CICheckRetryMaxGenerations, Observed: int(row.RetryGeneration) + 1}
	}
	_, err = s.q.GetCISourceCheckRetryByGeneration(ctx, tx, sqlc.GetCISourceCheckRetryByGenerationParams{AccountID: dbUUID(account), EventID: dbUUID(event), Generation: row.RetryGeneration + 1})
	if err == nil {
		return SourceCheckRetry{}, false, ErrCheckRetryConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SourceCheckRetry{}, false, storeError(err)
	}
	retry, err := s.q.InsertCISourceCheckRetry(ctx, tx, sqlc.InsertCISourceCheckRetryParams{ID: dbUUID(uuid.NewString()), AccountID: dbUUID(account), EventID: dbUUID(event), Generation: row.RetryGeneration + 1, KeyDigest: digest})
	if err != nil {
		return SourceCheckRetry{}, false, storeError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SourceCheckRetry{}, false, storeError(err)
	}
	return sourceCheckRetryRow(retry), false, nil
}

func (s *PostgresStore) attachSourceCheckDeliveries(ctx context.Context, account, binding string, events []AutomationEventSummary) ([]AutomationEventSummary, error) {
	hasSkip := false
	for _, event := range events {
		if event.PathDecision != nil && event.PathDecision.Outcome == "skip" {
			hasSkip = true
			break
		}
	}
	if !hasSkip {
		return events, nil
	}
	rows, err := s.SourceCheckDeliveries(ctx, account, binding)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]SourceCheckDelivery, len(rows))
	for _, row := range rows {
		byID[row.EventID] = row
	}
	for i := range events {
		if row, ok := byID[events[i].ID]; ok {
			events[i].CheckDelivery = &row
		}
	}
	return events, nil
}

var _ SourceCheckRecoveryStore = (*PostgresStore)(nil)

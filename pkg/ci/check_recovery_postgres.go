package ci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func checkRetryRow(row sqlc.CiExecutionCheckRetry) CheckRetry {
	out := CheckRetry{ID: uuidString(row.ID), ExecutionID: uuidString(row.ExecutionID), Generation: row.Generation, RequestedAt: row.RequestedAt.Time}
	if row.AppliedAt.Valid {
		out.AppliedAt = &row.AppliedAt.Time
	}
	return out
}

func (s *PostgresStore) CheckDeliveries(ctx context.Context, account string) ([]CheckDelivery, error) {
	rows, err := s.q.ListCICheckDeliveries(ctx, s.pool, sqlc.ListCICheckDeliveriesParams{AccountID: dbUUID(account), MaxRows: api.CIMaxJobsExecutionsPerAccount})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]CheckDelivery, 0, len(rows))
	for _, row := range rows {
		item := CheckDelivery{ExecutionID: uuidString(row.ExecutionID), Pipeline: row.Pipeline, CommitSHA: row.CommitSha,
			ExecutionStatus: row.ExecutionStatus, DeliveryStatus: row.DeliveryStatus, Attempts: int(row.Attempts), LastError: row.LastError,
			CheckID: row.CheckID, DesiredVersion: row.DesiredVersion, DeliveredVersion: row.DeliveredVersion,
			VersionPending:  row.DeliveryStatus != "not_requested" && row.DesiredVersion > row.DeliveredVersion,
			RetryGeneration: row.RetryGeneration, MaxRetries: api.CICheckRetryMaxGenerations}
		if row.RetryID.Valid {
			retry := checkRetryRow(sqlc.CiExecutionCheckRetry{ID: row.RetryID, ExecutionID: row.ExecutionID, Generation: row.RequestedGeneration, RequestedAt: row.RequestedAt, AppliedAt: row.AppliedAt})
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

func (s *PostgresStore) CheckDelivery(ctx context.Context, account, execution string) (CheckDelivery, error) {
	rows, err := s.CheckDeliveries(ctx, account)
	if err != nil {
		return CheckDelivery{}, err
	}
	for _, row := range rows {
		if row.ExecutionID == execution {
			return row, nil
		}
	}
	return CheckDelivery{}, ErrNotFound
}

func (s *PostgresStore) requestCheckRetry(ctx context.Context, account, execution, key string, req CheckRetryRequest) (CheckRetry, bool, error) {
	tx, err := s.accountTx(ctx, account)
	if err != nil {
		return CheckRetry{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Recheck local account authority under the account lock after the RPC.
	if _, err := s.q.CIJobsAccount(ctx, tx, dbUUID(account)); err != nil {
		return CheckRetry{}, false, ErrGitHubAccess
	}
	digest := sha256.Sum256([]byte(key))
	keyDigest := hex.EncodeToString(digest[:])
	existing, err := s.q.GetCICheckRetryByKey(ctx, tx, sqlc.GetCICheckRetryByKeyParams{AccountID: dbUUID(account), KeyDigest: keyDigest})
	if err == nil {
		if uuidString(existing.ExecutionID) != execution || existing.Generation != req.ExpectedRetryGeneration+1 {
			return CheckRetry{}, false, ErrCheckRetryConflict
		}
		return checkRetryRow(existing), true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CheckRetry{}, false, storeError(err)
	}
	row, err := s.q.TryLockCICheckDelivery(ctx, tx, sqlc.TryLockCICheckDeliveryParams{AccountID: dbUUID(account), ExecutionID: dbUUID(execution)})
	if errors.Is(err, pgx.ErrNoRows) {
		return CheckRetry{}, false, ErrCheckRetryConflict
	}
	if err != nil {
		return CheckRetry{}, false, storeError(err)
	}
	if !checkDeliveryExhausted(row.Status, row.LastError, int(row.Attempts)) || row.RetryGeneration != req.ExpectedRetryGeneration {
		return CheckRetry{}, false, ErrCheckRetryConflict
	}
	if row.RetryGeneration >= api.CICheckRetryMaxGenerations {
		return CheckRetry{}, false, &LimitError{Resource: "check_retry_generations", Limit: api.CICheckRetryMaxGenerations, Observed: int(row.RetryGeneration) + 1}
	}
	_, err = s.q.GetCICheckRetryByGeneration(ctx, tx, sqlc.GetCICheckRetryByGenerationParams{AccountID: dbUUID(account), ExecutionID: dbUUID(execution), Generation: row.RetryGeneration + 1})
	if err == nil {
		return CheckRetry{}, false, ErrCheckRetryConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CheckRetry{}, false, storeError(err)
	}
	retry, err := s.q.InsertCICheckRetry(ctx, tx, sqlc.InsertCICheckRetryParams{ID: dbUUID(uuid.NewString()), AccountID: dbUUID(account), ExecutionID: dbUUID(execution), Generation: row.RetryGeneration + 1, KeyDigest: keyDigest})
	if err != nil {
		return CheckRetry{}, false, storeError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CheckRetry{}, false, storeError(err)
	}
	return checkRetryRow(retry), false, nil
}

var _ CheckRecoveryStore = (*PostgresStore)(nil)

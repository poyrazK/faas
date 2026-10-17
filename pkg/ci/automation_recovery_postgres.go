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

func automationRetryRow(row sqlc.CiAutomationEventRetry) AutomationRetry {
	return AutomationRetry{ID: uuidString(row.ID), EventID: uuidString(row.EventID), Generation: row.Generation,
		PreviousAttempts: int(row.PreviousAttempts), PreviousError: row.PreviousError, RequestedAt: row.RequestedAt.Time}
}

func (s *PostgresStore) AutomationEventStatus(ctx context.Context, account, binding, id string) (AutomationEventSummary, error) {
	rows, err := s.AutomationEvents(ctx, account, binding)
	if err != nil {
		return AutomationEventSummary{}, err
	}
	for _, row := range rows {
		if row.ID == id {
			return row, nil
		}
	}
	return AutomationEventSummary{}, ErrNotFound
}

func (s *PostgresStore) automationRetrySource(ctx context.Context, account, binding, id string) (AutomationEvent, error) {
	row, err := s.q.GetCIAutomationRecoveryEvent(ctx, s.pool, sqlc.GetCIAutomationRecoveryEventParams{AccountID: dbUUID(account), BindingID: dbUUID(binding), ID: dbUUID(id)})
	if err != nil {
		return AutomationEvent{}, storeError(err)
	}
	return automationEventRow(row)
}

func (s *PostgresStore) requestAutomationRetry(ctx context.Context, account, binding, id, key string, req AutomationRetryRequest) (AutomationRetry, bool, error) {
	tx, err := s.accountTx(ctx, account)
	if err != nil {
		return AutomationRetry{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.jobsAccount(ctx, tx, account); err != nil {
		return AutomationRetry{}, false, err
	}
	// Bind even response replays to the exact account, binding and source event.
	if _, err := s.q.GetCIAutomationRecoveryEvent(ctx, tx, sqlc.GetCIAutomationRecoveryEventParams{AccountID: dbUUID(account), BindingID: dbUUID(binding), ID: dbUUID(id)}); err != nil {
		return AutomationRetry{}, false, storeError(err)
	}
	digest := sha256.Sum256([]byte(key))
	keyDigest := hex.EncodeToString(digest[:])
	existing, err := s.q.GetCIAutomationRetryByKey(ctx, tx, sqlc.GetCIAutomationRetryByKeyParams{AccountID: dbUUID(account), KeyDigest: keyDigest})
	if err == nil {
		if uuidString(existing.EventID) != id || existing.Generation != req.ExpectedRetryGeneration+1 {
			return AutomationRetry{}, false, ErrAutomationRetryConflict
		}
		return automationRetryRow(existing), true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AutomationRetry{}, false, storeError(err)
	}
	row, err := s.q.TryLockCIAutomationRecoveryEvent(ctx, tx, sqlc.TryLockCIAutomationRecoveryEventParams{AccountID: dbUUID(account), BindingID: dbUUID(binding), ID: dbUUID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return AutomationRetry{}, false, ErrAutomationRetryConflict
	}
	if err != nil {
		return AutomationRetry{}, false, storeError(err)
	}
	if !automationAdmissionExhausted(row.Status, row.LastError, int(row.Attempts)) || row.ExecutionID.Valid || row.LeaseToken.Valid || row.LeaseUntil.Valid || row.RetryGeneration != req.ExpectedRetryGeneration {
		return AutomationRetry{}, false, ErrAutomationRetryConflict
	}
	if row.RetryGeneration >= api.CIAutomationRetryMaxGenerations {
		return AutomationRetry{}, false, &LimitError{Resource: "automation_retry_generations", Limit: api.CIAutomationRetryMaxGenerations, Observed: int(row.RetryGeneration) + 1}
	}
	if err := s.validateAutomationRetryAuthority(ctx, tx, account, row); err != nil {
		return AutomationRetry{}, false, err
	}
	retry, err := s.q.InsertCIAutomationRetry(ctx, tx, sqlc.InsertCIAutomationRetryParams{ID: dbUUID(uuid.NewString()), AccountID: dbUUID(account), EventID: dbUUID(id),
		Generation: row.RetryGeneration + 1, KeyDigest: keyDigest, PreviousAttempts: row.Attempts, PreviousError: row.LastError})
	if err != nil {
		return AutomationRetry{}, false, storeError(err)
	}
	count, err := s.q.ResetCIAutomationEventForRetry(ctx, tx, sqlc.ResetCIAutomationEventForRetryParams{AccountID: dbUUID(account), BindingID: dbUUID(binding), ID: dbUUID(id),
		RetryGeneration: row.RetryGeneration, MaxAttempts: api.CIGitHubMaxAttempts})
	if err != nil {
		return AutomationRetry{}, false, storeError(err)
	}
	if count != 1 {
		return AutomationRetry{}, false, ErrAutomationRetryConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return AutomationRetry{}, false, storeError(err)
	}
	return automationRetryRow(retry), false, nil
}

func (s *PostgresStore) validateAutomationRetryAuthority(ctx context.Context, tx pgx.Tx, account string, row sqlc.CiAutomationEvent) error {
	event, err := automationEventRow(row)
	if err != nil {
		return err
	}
	hasExecution, err := s.q.CIAutomationEventHasExecution(ctx, tx, sqlc.CIAutomationEventHasExecutionParams{AccountID: dbUUID(account), EventID: event.ID})
	if err != nil {
		return storeError(err)
	}
	policyRow, err := s.q.GetCIAutomationPolicy(ctx, tx, sqlc.GetCIAutomationPolicyParams{AccountID: dbUUID(account), BindingID: dbUUID(event.Binding.ID)})
	if err != nil {
		return storeError(err)
	}
	policy, err := automationPolicyRow(policyRow)
	if err != nil {
		return err
	}
	if hasExecution || !policy.Enabled || policy.ID != event.Policy.ID || policy.Generation != event.Policy.Generation || policy.ProfileID != event.Policy.ProfileID ||
		policy.BindingGeneration != event.Binding.Generation || !automationPolicyMatches(policy, event.Source.GitHubEvent) {
		return ErrAutomationRetryConflict
	}
	if err := s.validateAutomationProfile(ctx, tx, account, event.Binding, event.Policy.ProfileID); err != nil {
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrGitHubIgnored) {
			return ErrAutomationRetryConflict
		}
		return err
	}
	return nil
}

var _ AutomationRecoveryStore = (*PostgresStore)(nil)

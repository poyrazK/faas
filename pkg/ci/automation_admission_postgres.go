package ci

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func automationEventRow(row sqlc.CiAutomationEvent) (AutomationEvent, error) {
	var event AutomationEvent
	if err := json.Unmarshal(row.Snapshot, &event); err != nil {
		return event, errors.New("decode CI automation event")
	}
	if event.ID != uuidString(row.ID) || event.AccountID != uuidString(row.AccountID) || event.Policy.ID != uuidString(row.PolicyID) ||
		event.Policy.Generation != row.PolicyGeneration || event.Policy.ProfileID != uuidString(row.ProfileID) ||
		event.Binding.ID != uuidString(row.BindingID) || event.Binding.AccountID != event.AccountID || event.Policy.AccountID != event.AccountID ||
		event.Policy.BindingID != event.Binding.ID || event.Policy.BindingGeneration != event.Binding.Generation ||
		event.Source.BindingID != event.Binding.ID || event.Source.Generation != event.Binding.Generation ||
		!bindingMatches(event.Binding, event.Source.GitHubEvent) || !automationPolicyMatches(event.Policy, event.Source.GitHubEvent) || event.Source.Key() != row.EventKey {
		return AutomationEvent{}, errors.New("CI automation source identity mismatch")
	}
	event.Status, event.Attempts = row.Status, int(row.Attempts)
	if row.LeaseToken.Valid {
		event.LeaseToken = uuidString(row.LeaseToken)
	}
	return event, nil
}

func (s *PostgresStore) ClaimAutomationEvent(ctx context.Context, account string) (AutomationEvent, error) {
	if err := s.q.ExpireCIAutomationEvents(ctx, s.pool, sqlc.ExpireCIAutomationEventsParams{AccountID: dbUUID(account), MaxAttempts: api.CIGitHubMaxAttempts}); err != nil {
		return AutomationEvent{}, storeError(err)
	}
	row, err := s.q.ClaimCIAutomationEvent(ctx, s.pool, sqlc.ClaimCIAutomationEventParams{AccountID: dbUUID(account), MaxAttempts: api.CIGitHubMaxAttempts,
		Token: dbUUID(uuid.NewString()), LeaseSeconds: api.CIGitHubLeaseSeconds})
	if errors.Is(err, pgx.ErrNoRows) {
		return AutomationEvent{}, ErrNoGitHubWork
	}
	if err != nil {
		return AutomationEvent{}, storeError(err)
	}
	return automationEventRow(row)
}

func (s *PostgresStore) jobsAutomation(ctx context.Context, tx pgx.Tx, account, key string, req JobsSubmitRequest, binding GitHubBinding) error {
	if req.automation == nil {
		return nil
	}
	claim := req.automation
	row, err := s.q.LockCIAutomationEvent(ctx, tx, sqlc.LockCIAutomationEventParams{AccountID: dbUUID(account), ID: dbUUID(claim.ID)})
	if err != nil {
		return storeError(err)
	}
	event, err := automationEventRow(row)
	if err != nil {
		return err
	}
	now, err := s.q.CIExecutionClock(ctx, tx)
	if err != nil {
		return storeError(err)
	}
	if event.Status != "processing" || event.LeaseToken != claim.LeaseToken || !now.Time.Before(row.LeaseUntil.Time) {
		return ErrLeaseLost
	}
	policy, err := s.q.GetCIAutomationPolicy(ctx, tx, sqlc.GetCIAutomationPolicyParams{AccountID: dbUUID(account), BindingID: dbUUID(binding.ID)})
	if err != nil {
		return storeError(err)
	}
	if !policy.Enabled || uuidString(policy.ID) != event.Policy.ID || policy.Generation != event.Policy.Generation || policy.BindingGeneration != binding.Generation ||
		policy.LatestOnly != event.Policy.LatestOnly || !reflect.DeepEqual(claim.Policy, event.Policy) ||
		uuidString(policy.ProfileID) != req.ProfileID || req.ProfileID != event.Policy.ProfileID || req.CommitSHA != event.Source.CommitSHA ||
		key != "github-auto:"+event.ID || !sameJobsBinding(binding, event.Binding) || !sameJobsBinding(claim.Binding, event.Binding) ||
		!reflect.DeepEqual(claim.Source, event.Source) || !reflect.DeepEqual(automationProvenance(claim), automationProvenance(&event)) {
		return ErrGitHubIgnored
	}
	return nil
}

func (s *PostgresStore) finishAutomationAdmission(ctx context.Context, tx pgx.Tx, claim *AutomationEvent, executionID string, decision *AutomationPathDecision) error {
	if claim == nil {
		return nil
	}
	var raw []byte
	if decision != nil {
		raw, _ = json.Marshal(decision)
	}
	count, err := s.q.AdmitCIAutomationEvent(ctx, tx, sqlc.AdmitCIAutomationEventParams{AccountID: dbUUID(claim.AccountID), ID: dbUUID(claim.ID),
		LeaseToken: dbUUID(claim.LeaseToken), ExecutionID: dbUUID(executionID), PathDecision: raw})
	if err != nil {
		return storeError(err)
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) FailAutomationEvent(ctx context.Context, claim AutomationEvent, reason string, permanent bool) error {
	now, err := s.q.CIExecutionClock(ctx, s.pool)
	if err != nil {
		return storeError(err)
	}
	status := "pending"
	if permanent {
		status = "ignored"
	} else if claim.Attempts >= api.CIGitHubMaxAttempts {
		status = "dead"
	}
	count, err := s.q.FailCIAutomationEvent(ctx, s.pool, sqlc.FailCIAutomationEventParams{AccountID: dbUUID(claim.AccountID), ID: dbUUID(claim.ID), LeaseToken: dbUUID(claim.LeaseToken),
		Status: status, LastError: reason, NextAttemptAt: timestamp(githubRetry(claim.Attempts, now.Time))})
	if err != nil {
		return storeError(err)
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) AutomationEvents(ctx context.Context, account, binding string) ([]AutomationEventSummary, error) {
	rows, err := s.q.ListCIAutomationEvents(ctx, s.pool, sqlc.ListCIAutomationEventsParams{AccountID: dbUUID(account), BindingID: dbUUID(binding), MaxRows: api.CIGitHubMaxTriggersPerAccount})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]AutomationEventSummary, 0, len(rows))
	for _, row := range rows {
		event := AutomationEventSummary{ID: uuidString(row.ID), PolicyID: uuidString(row.PolicyID), PolicyGeneration: row.PolicyGeneration,
			ProfileID: uuidString(row.ProfileID), CommitSHA: row.CommitSha, Branch: row.Branch, Kind: row.Kind, PRNumber: row.PrNumber, Status: row.Status, Attempts: int(row.Attempts), LastError: row.LastError, CreatedAt: row.CreatedAt.Time}
		if row.ExecutionID.Valid {
			event.ExecutionID = uuidString(row.ExecutionID)
		}
		if len(row.PathDecision) > 0 {
			if json.Unmarshal(row.PathDecision, &event.PathDecision) != nil {
				return nil, errors.New("decode CI path decision")
			}
		}
		event.RetryGeneration, event.MaxRetries = row.RetryGeneration, api.CIAutomationRetryMaxGenerations
		if row.SupersededBy.Valid {
			event.SupersededBy = uuidString(row.SupersededBy)
		}
		if row.SupersededSha.Valid {
			event.SupersededSHA = row.SupersededSha.String
		}
		if row.SupersededAt.Valid {
			event.SupersededAt = &row.SupersededAt.Time
		}
		event.Retryable = automationAdmissionExhausted(row.Status, row.LastError, int(row.Attempts)) && !row.ExecutionID.Valid && !row.HasExecution && !row.LeaseUntil.Valid && row.RetryGeneration < api.CIAutomationRetryMaxGenerations
		if row.Status == "pending" || row.Status == "processing" {
			event.NextAttemptAt = &row.NextAttemptAt.Time
		}
		if row.LeaseUntil.Valid {
			event.LeaseExpiresAt = &row.LeaseUntil.Time
		}
		if row.RetryID.Valid {
			retry := automationRetryRow(sqlc.CiAutomationEventRetry{ID: row.RetryID, EventID: row.ID, Generation: row.LastRetryGeneration,
				PreviousAttempts: row.PreviousAttempts, PreviousError: row.PreviousError, RequestedAt: row.RequestedAt})
			event.LastRetry = &retry
		}
		out = append(out, event)
	}
	return s.attachSourceCheckDeliveries(ctx, account, binding, out)
}

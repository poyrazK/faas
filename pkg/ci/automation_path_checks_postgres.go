package ci

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func (s *PostgresStore) claimAutomationPathCheck(ctx context.Context, account string) (executionCheckClaim, error) {
	if err := s.q.ExpireCIAutomationPathChecks(ctx, s.pool, sqlc.ExpireCIAutomationPathChecksParams{AccountID: dbUUID(account), MaxAttempts: api.CIGitHubMaxAttempts}); err != nil {
		return executionCheckClaim{}, storeError(err)
	}
	if err := s.q.ApplyCISourceCheckRetry(ctx, s.pool, sqlc.ApplyCISourceCheckRetryParams{AccountID: dbUUID(account), MaxAttempts: api.CIGitHubMaxAttempts}); err != nil {
		return executionCheckClaim{}, storeError(err)
	}
	row, err := s.q.ClaimCIAutomationPathCheck(ctx, s.pool, sqlc.ClaimCIAutomationPathCheckParams{AccountID: dbUUID(account), MaxAttempts: api.CIGitHubMaxAttempts, Token: dbUUID(uuid.NewString()), LeaseSeconds: api.CIGitHubLeaseSeconds})
	if errors.Is(err, pgx.ErrNoRows) {
		return executionCheckClaim{}, ErrNoGitHubWork
	}
	return executionCheckClaim{account: account, execution: uuidString(row.EventID), token: uuidString(row.LeaseToken), source: true}, storeError(err)
}

// The same serialized publisher and bounded retry policy serve execution and
// skipped-source projections. Admission never locks this delivery-only row.
func (s *PostgresStore) deliverAutomationPathCheck(ctx context.Context, claim executionCheckClaim, verify GitHubVerifier, publish ExecutionCheckPublisher) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.q.LockCIAutomationPathCheck(ctx, tx, sqlc.LockCIAutomationPathCheckParams{AccountID: dbUUID(claim.account), EventID: dbUUID(claim.execution)})
	if err != nil {
		return storeError(err)
	}
	now, err := s.q.CIExecutionClock(ctx, tx)
	if err != nil {
		return storeError(err)
	}
	if row.Status != "sending" || uuidString(row.LeaseToken) != claim.token || !now.Time.Before(row.LeaseUntil.Time) {
		return ErrLeaseLost
	}
	var check ExecutionCheck
	if json.Unmarshal(row.Snapshot, &check) != nil || !check.Valid() || check.AccountID != claim.account || check.AutomationEventID != claim.execution || check.CheckID != 0 {
		return ErrConflict
	}
	check.CheckID = row.CheckID
	workCtx, cancel := context.WithTimeout(ctx, api.CIGitHubRPCSeconds*time.Second)
	err = verify(workCtx, check.AccountID, check.InstallationID, check.RepositoryID, check.Repository)
	var id int64
	if err == nil {
		id, err = publish(workCtx, check)
	}
	cancel()
	if err == nil && (id <= 0 || row.CheckID > 0 && id != row.CheckID) {
		err = ErrGitHubUnavailable
	}
	if err != nil {
		reason := "publish_failed"
		if errors.Is(err, ErrGitHubAccess) {
			reason = "access_denied"
		}
		if errors.Is(err, ErrGitHubUnavailable) {
			reason = "unavailable"
		}
		n, saveErr := s.q.FailCIAutomationPathCheck(ctx, tx, sqlc.FailCIAutomationPathCheckParams{AccountID: row.AccountID, EventID: row.EventID, LeaseToken: row.LeaseToken,
			Status: githubFailureState(int(row.Attempts), reason), LastError: reason, NextAttemptAt: timestamp(githubRetry(int(row.Attempts), now.Time))})
		if saveErr != nil {
			return storeError(saveErr)
		}
		if n != 1 {
			return ErrLeaseLost
		}
		if err := tx.Commit(ctx); err != nil {
			return storeError(err)
		}
		if reason == "access_denied" {
			return nil
		}
		return errors.New("CI skipped source Check delivery unavailable")
	}
	n, err := s.q.FinishCIAutomationPathCheck(ctx, tx, sqlc.FinishCIAutomationPathCheckParams{AccountID: row.AccountID, EventID: row.EventID, LeaseToken: row.LeaseToken, CheckID: id})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return storeError(tx.Commit(ctx))
}

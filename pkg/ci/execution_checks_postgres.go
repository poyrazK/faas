package ci

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func (s *PostgresStore) claimExecutionCheck(ctx context.Context, account string) (executionCheckClaim, error) {
	if err := s.q.ExpireCIExecutionChecks(ctx, s.pool, sqlc.ExpireCIExecutionChecksParams{AccountID: dbUUID(account), MaxAttempts: api.CIGitHubMaxAttempts}); err != nil {
		return executionCheckClaim{}, storeError(err)
	}
	if err := s.q.ApplyCICheckRetry(ctx, s.pool, sqlc.ApplyCICheckRetryParams{AccountID: dbUUID(account), MaxAttempts: api.CIGitHubMaxAttempts}); err != nil {
		return executionCheckClaim{}, storeError(err)
	}
	c, err := s.q.ClaimCIExecutionCheck(ctx, s.pool, sqlc.ClaimCIExecutionCheckParams{AccountID: dbUUID(account), MaxAttempts: api.CIGitHubMaxAttempts, Token: dbUUID(uuid.NewString()), LeaseSeconds: api.CIGitHubLeaseSeconds})
	if errors.Is(err, pgx.ErrNoRows) {
		return s.claimAutomationPathCheck(ctx, account)
	}
	return executionCheckClaim{account: account, execution: uuidString(c.ExecutionID), token: uuidString(c.LeaseToken)}, storeError(err)
}

// This transaction locks only delivery bookkeeping. Execution writers never
// acquire this row, and other publishers use SKIP LOCKED. The claim was already
// committed, so a crash consumes its retry and can recover after lease expiry.
func (s *PostgresStore) deliverExecutionCheck(ctx context.Context, claim executionCheckClaim, verify GitHubVerifier, publish ExecutionCheckPublisher) error {
	if claim.source {
		return s.deliverAutomationPathCheck(ctx, claim, verify, publish)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	c, err := s.q.LockCIExecutionCheck(ctx, tx, sqlc.LockCIExecutionCheckParams{AccountID: dbUUID(claim.account), ExecutionID: dbUUID(claim.execution)})
	if err != nil {
		return storeError(err)
	}
	now, err := s.q.CIExecutionClock(ctx, tx)
	if err != nil {
		return storeError(err)
	}
	if c.Status != "sending" || uuidString(c.LeaseToken) != claim.token || !now.Time.Before(c.LeaseUntil.Time) {
		return ErrLeaseLost
	}
	stored, err := s.q.GetCIExecution(ctx, tx, sqlc.GetCIExecutionParams{AccountID: dbUUID(claim.account), ID: dbUUID(claim.execution)})
	if err != nil {
		return storeError(err)
	}
	row, err := executionRow(stored)
	if err != nil {
		return err
	}
	check, err := executionCheck(row)
	if err != nil {
		return err
	}
	check.CheckID = c.CheckID
	digest := check.digest()
	id := c.CheckID
	if digest != c.DeliveredDigest {
		workCtx, cancel := context.WithTimeout(ctx, api.CIGitHubRPCSeconds*time.Second)
		err = verify(workCtx, check.AccountID, check.InstallationID, check.RepositoryID, check.Repository)
		if err == nil {
			id, err = publish(workCtx, check)
		}
		cancel()
		if err == nil && (id <= 0 || c.CheckID > 0 && id != c.CheckID) {
			err = fmt.Errorf("CI execution Check response identity missing")
		}
	}
	if err != nil {
		return s.failExecutionCheck(ctx, tx, c, claim, err)
	}
	n, err := s.q.FinishCIExecutionCheck(ctx, tx, sqlc.FinishCIExecutionCheckParams{AccountID: dbUUID(claim.account), ExecutionID: dbUUID(claim.execution), LeaseToken: dbUUID(claim.token), DeliveredVersion: check.Version, DeliveredDigest: digest, CheckID: id})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return storeError(tx.Commit(ctx))
}

func (s *PostgresStore) failExecutionCheck(ctx context.Context, tx pgx.Tx, c sqlc.CiExecutionCheck, claim executionCheckClaim, cause error) error {
	now, err := s.q.CIExecutionClock(ctx, tx)
	if err != nil {
		return storeError(err)
	}
	reason := "publish_failed"
	if errors.Is(cause, ErrGitHubAccess) {
		reason = "access_denied"
	}
	if errors.Is(cause, ErrGitHubUnavailable) {
		reason = "unavailable"
	}
	n, err := s.q.FailCIExecutionCheck(ctx, tx, sqlc.FailCIExecutionCheckParams{AccountID: dbUUID(claim.account), ExecutionID: dbUUID(claim.execution), LeaseToken: dbUUID(claim.token), Status: githubFailureState(int(c.Attempts), reason), LastError: reason, NextAttemptAt: timestamp(githubRetry(int(c.Attempts), now.Time))})
	if err != nil {
		return storeError(err)
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
	return errors.New("CI execution Check delivery unavailable")
}

var _ ExecutionCheckStore = (*PostgresStore)(nil)

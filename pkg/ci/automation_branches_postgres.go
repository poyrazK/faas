package ci

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func (s *PostgresStore) beginAutomationHead(ctx context.Context, event AutomationEvent) (int64, error) {
	if event.Source.Kind != "push" {
		return 0, ErrConflict
	}
	return s.beginAutomationObservation(ctx, event)
}

func (s *PostgresStore) beginAutomationObservation(ctx context.Context, event AutomationEvent) (int64, error) {
	tx, err := s.accountTx(ctx, event.AccountID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	req := JobsSubmitRequest{ProfileID: event.Policy.ProfileID, SubmitRequest: SubmitRequest{CommitSHA: event.Source.CommitSHA}, automation: &event}
	if err := s.jobsAutomation(ctx, tx, event.AccountID, "github-auto:"+event.ID, req, event.Binding); err != nil {
		return 0, err
	}
	row, err := s.q.GetCIAutomationRecoveryEvent(ctx, tx, sqlc.GetCIAutomationRecoveryEventParams{AccountID: dbUUID(event.AccountID), BindingID: dbUUID(event.Binding.ID), ID: dbUUID(event.ID)})
	if err != nil {
		return 0, storeError(err)
	}
	if err := s.validateAutomationRetryAuthority(ctx, tx, event.AccountID, row); err != nil {
		if errors.Is(err, ErrAutomationRetryConflict) {
			return 0, ErrGitHubIgnored
		}
		return 0, err
	}
	var fence int64
	if event.Source.Kind == "pull_request" {
		fence, err = s.q.BeginCIAutomationPullRequest(ctx, tx, sqlc.BeginCIAutomationPullRequestParams{AccountID: dbUUID(event.AccountID), BindingID: dbUUID(event.Binding.ID), PrNumber: int64(event.Source.PRNumber)})
	} else {
		fence, err = s.q.BeginCIAutomationHead(ctx, tx, sqlc.BeginCIAutomationHeadParams{AccountID: dbUUID(event.AccountID), BindingID: dbUUID(event.Binding.ID), Branch: event.Source.Branch})
	}
	if err != nil {
		return 0, storeError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, storeError(err)
	}
	return fence, nil
}

// Called only after normal source/profile checks under the account lock. All
// cancellations use the engine's existing version and runtime capability fences.
func (s *PostgresStore) automationBranchAdmission(ctx context.Context, tx pgx.Tx, req JobsSubmitRequest) (bool, error) {
	if req.automation == nil {
		return true, nil
	}
	if req.automation.Source.Kind == "pull_request" {
		return s.automationPullRequestAdmission(ctx, tx, req)
	}
	event := *req.automation
	if event.Policy.LatestOnly {
		if req.automationHead == nil {
			return false, ErrGitHubUnavailable
		}
		current, err := s.q.GetCIAutomationHeadFence(ctx, tx, sqlc.GetCIAutomationHeadFenceParams{AccountID: dbUUID(event.AccountID), BindingID: dbUUID(event.Binding.ID), Branch: event.Source.Branch})
		if err != nil {
			return false, storeError(err)
		}
		if current != req.automationHead.Fence {
			return false, s.deferAutomationBranch(ctx, tx, event)
		}
		if req.automationHead.SHA != event.Source.CommitSHA {
			return false, s.ignoreAutomationOldHead(ctx, tx, event, req.automationHead.SHA)
		}
		// An incremental push diff says nothing about older untested changes.
		// A skipped push therefore never supersedes queued or running work.
		if req.automationPaths != nil && req.automationPaths.Outcome == "skip" {
			pending, err := s.q.CIAutomationUnsettledPushSources(ctx, tx, sqlc.CIAutomationUnsettledPushSourcesParams{AccountID: dbUUID(event.AccountID), BindingID: dbUUID(event.Binding.ID), PolicyID: dbUUID(event.Policy.ID), PolicyGeneration: event.Policy.Generation, ID: dbUUID(event.ID), Branch: event.Source.Branch})
			if err != nil {
				return false, storeError(err)
			}
			if !pending {
				return true, nil
			}
			req.automationPaths.Outcome, req.automationPaths.Reason = "run", "prior_source_unsettled"
		}
		if err := s.q.SupersedeCIAutomationQueued(ctx, tx, sqlc.SupersedeCIAutomationQueuedParams{AccountID: dbUUID(event.AccountID), BindingID: dbUUID(event.Binding.ID),
			PolicyID: dbUUID(event.Policy.ID), PolicyGeneration: event.Policy.Generation, Branch: event.Source.Branch, HeadEvent: dbUUID(event.ID), HeadSha: event.Source.CommitSHA}); err != nil {
			return false, storeError(err)
		}
	}
	if req.automationPaths != nil && req.automationPaths.Outcome == "skip" {
		return true, nil
	}
	rows, err := s.q.LockCIAutomationBranchExecutions(ctx, tx, sqlc.LockCIAutomationBranchExecutionsParams{AccountID: dbUUID(event.AccountID), BindingID: dbUUID(event.Binding.ID), Branch: event.Source.Branch})
	if err != nil {
		return false, storeError(err)
	}
	blocked, err := s.automationExecutionsBlocked(ctx, tx, event, rows, event.Policy.LatestOnly, api.CIAutomationActivePerBranch)
	if err != nil {
		return false, err
	}
	if blocked {
		return false, s.deferAutomationBranch(ctx, tx, event)
	}
	return true, nil
}

func (s *PostgresStore) automationExecutionsBlocked(ctx context.Context, tx pgx.Tx, event AutomationEvent, rows []sqlc.CiExecution, latest bool, limit int) (bool, error) {
	blocked, cancelled := 0, false
	for _, stored := range rows {
		row, err := executionRow(stored)
		if err != nil {
			return false, err
		}
		e := row.Execution
		if e.Automation == nil {
			return false, ErrConflict
		}
		if latest && e.Automation.PolicyID == event.Policy.ID && e.Automation.PolicyGeneration == event.Policy.Generation && !terminal(e.Status) {
			cancelled = cancelled || !e.CancelRequested
			if err := s.supersedeAutomationExecution(ctx, tx, &row, event); err != nil {
				return false, err
			}
		}
		safe, err := s.q.CIAutomationExecutionStopped(ctx, tx, sqlc.CIAutomationExecutionStoppedParams{AccountID: dbUUID(event.AccountID), ExecutionID: stored.ID})
		if err != nil {
			return false, storeError(err)
		}
		if !terminal(row.Execution.Status) || !safe.Valid || !safe.Bool {
			blocked++
		}
	}
	return cancelled || blocked >= limit, nil
}

func (s *PostgresStore) supersedeAutomationExecution(ctx context.Context, tx pgx.Tx, row *executionRecord, event AutomationEvent) error {
	if err := s.q.MarkCIAutomationSupersededExecution(ctx, tx, sqlc.MarkCIAutomationSupersededExecutionParams{AccountID: dbUUID(event.AccountID), ExecutionID: dbUUID(row.Execution.ID), SupersededBy: dbUUID(event.ID), SupersededSha: pgtype.Text{String: event.Source.CommitSHA, Valid: true}}); err != nil {
		return storeError(err)
	}
	if row.Execution.CancelRequested {
		return nil
	}
	return s.mutateLockedExecution(ctx, tx, row, func(row *executionRecord, now time.Time) error {
		cancelExecutionRecord(row, now)
		return nil
	})
}

func (s *PostgresStore) deferAutomationBranch(ctx context.Context, tx pgx.Tx, event AutomationEvent) error {
	return s.deferAutomationScope(ctx, tx, event, "branch_wait")
}

func (s *PostgresStore) deferAutomationScope(ctx context.Context, tx pgx.Tx, event AutomationEvent, reason string) error {
	count, err := s.q.DeferCIAutomationScope(ctx, tx, sqlc.DeferCIAutomationScopeParams{AccountID: dbUUID(event.AccountID), ID: dbUUID(event.ID), LeaseToken: dbUUID(event.LeaseToken), PollSeconds: api.CIGitHubPollSeconds, Reason: reason})
	if err != nil {
		return storeError(err)
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) ignoreAutomationOldHead(ctx context.Context, tx pgx.Tx, event AutomationEvent, sha string) error {
	head, err := s.q.FindCIAutomationHeadEvent(ctx, tx, sqlc.FindCIAutomationHeadEventParams{AccountID: dbUUID(event.AccountID), BindingID: dbUUID(event.Binding.ID), PolicyID: dbUUID(event.Policy.ID), PolicyGeneration: event.Policy.Generation, Branch: event.Source.Branch, CommitSha: sha})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return storeError(err)
	}
	reason := "head_changed"
	if sha == "" {
		reason = "branch_deleted"
	}
	count, err := s.q.IgnoreCIAutomationOldHead(ctx, tx, sqlc.IgnoreCIAutomationOldHeadParams{AccountID: dbUUID(event.AccountID), ID: dbUUID(event.ID), LeaseToken: dbUUID(event.LeaseToken), LastError: reason, HeadSha: sha, SupersededBy: head})
	if err != nil {
		return storeError(err)
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

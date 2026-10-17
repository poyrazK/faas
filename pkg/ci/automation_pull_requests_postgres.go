package ci

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

func (s *PostgresStore) beginAutomationPullRequest(ctx context.Context, event AutomationEvent) (int64, error) {
	if event.Source.Kind != "pull_request" {
		return 0, ErrConflict
	}
	return s.beginAutomationObservation(ctx, event)
}

// PRs always use a freshly verified current head, independently of the optional
// push latest-only policy. An ineligible candidate cannot cancel other work.
func (s *PostgresStore) automationPullRequestAdmission(ctx context.Context, tx pgx.Tx, req JobsSubmitRequest) (bool, error) {
	event := *req.automation
	observation := req.automationPR
	if observation == nil || !observation.State.Valid(int64(event.Source.PRNumber)) {
		return false, ErrGitHubUnavailable
	}
	fence, err := s.q.GetCIAutomationPullRequestFence(ctx, tx, sqlc.GetCIAutomationPullRequestFenceParams{AccountID: dbUUID(event.AccountID), BindingID: dbUUID(event.Binding.ID), PrNumber: int64(event.Source.PRNumber)})
	if err != nil {
		return false, storeError(err)
	}
	if fence != observation.Fence {
		return false, s.deferAutomationScope(ctx, tx, event, "pr_wait")
	}
	state := observation.State
	if reason := pullRequestIneligible(event, state); reason != "" {
		return false, s.ignoreAutomationPullRequest(ctx, tx, event, reason, "")
	}
	if state.HeadSHA != event.Source.CommitSHA {
		return false, s.ignoreAutomationPullRequest(ctx, tx, event, "head_changed", state.HeadSHA)
	}
	if err := s.q.SupersedeCIAutomationPullRequestQueued(ctx, tx, sqlc.SupersedeCIAutomationPullRequestQueuedParams{AccountID: dbUUID(event.AccountID), BindingID: dbUUID(event.Binding.ID),
		PolicyID: dbUUID(event.Policy.ID), PolicyGeneration: event.Policy.Generation, PrNumber: int64(event.Source.PRNumber), HeadEvent: dbUUID(event.ID), HeadSha: event.Source.CommitSHA}); err != nil {
		return false, storeError(err)
	}
	rows, err := s.q.LockCIAutomationPullRequestExecutions(ctx, tx, sqlc.LockCIAutomationPullRequestExecutionsParams{AccountID: dbUUID(event.AccountID), BindingID: dbUUID(event.Binding.ID), PrNumber: int64(event.Source.PRNumber)})
	if err != nil {
		return false, storeError(err)
	}
	blocked, err := s.automationExecutionsBlocked(ctx, tx, event, rows, true, api.CIAutomationActivePerPullRequest)
	if err != nil {
		return false, err
	}
	if blocked {
		return false, s.deferAutomationScope(ctx, tx, event, "pr_wait")
	}
	return true, nil
}

func (s *PostgresStore) ignoreAutomationPullRequest(ctx context.Context, tx pgx.Tx, event AutomationEvent, reason, sha string) error {
	head, err := s.q.FindCIAutomationPullRequestHead(ctx, tx, sqlc.FindCIAutomationPullRequestHeadParams{AccountID: dbUUID(event.AccountID), BindingID: dbUUID(event.Binding.ID),
		PolicyID: dbUUID(event.Policy.ID), PolicyGeneration: event.Policy.Generation, PrNumber: int64(event.Source.PRNumber), Branch: event.Source.Branch, CommitSha: sha})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return storeError(err)
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

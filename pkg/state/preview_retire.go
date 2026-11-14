package state

import (
	"context"
	"fmt"
	"time"
)

// ProjectPRPreviewRetirer marks a project's open PR previews for teardown when
// its GitHub policy turns previews off. githubd then ignores the PRs' pushes,
// so the previews would otherwise serve their last commit to the end of their
// lease. The preview janitor tears them down on its next pass; the branch
// sweeper deletes the project's pull request branches (ADR-20261113121342),
// which the count does not include.
type ProjectPRPreviewRetirer interface {
	RetireProjectPRPreviews(ctx context.Context, accountID, projectID string) (int, error)
}

var (
	_ ProjectPRPreviewRetirer = (*PgStore)(nil)
	_ ProjectPRPreviewRetirer = (*MemStore)(nil)
)

func (s *PgStore) RetireProjectPRPreviews(ctx context.Context, accountID, projectID string) (int, error) {
	if accountID == "" || projectID == "" {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		update apps set preview_pr_state = $3, preview_expires_at = now()
		 where account_id = $1 and project_id = $2::uuid
		   and preview_of_slug is not null
		   and coalesce(preview_pr_number, 0) > 0
		   and status <> 'deleted'
		   and coalesce(preview_pr_state, '') not in ($3, $4, $5)`,
		accountID, projectID, PreviewPrStateStale, PreviewPrStateTearingDown, PreviewPrStateTornDown)
	if err != nil {
		return 0, fmt.Errorf("state: retire project PR previews: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
		update project_environment_branches b
		   set expires_at = greatest(b.created_at + interval '1 second', least(b.expires_at, now()))
		 where b.account_id = $1 and b.project_id = $2::uuid and b.pull_request_number is not null`,
		accountID, projectID); err != nil {
		return 0, fmt.Errorf("state: retire project PR branches: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (m *MemStore) RetireProjectPRPreviews(_ context.Context, accountID, projectID string) (int, error) {
	if accountID == "" || projectID == "" {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	retired := 0
	for id, app := range m.apps {
		if app.AccountID != accountID || app.ProjectID != projectID || app.PreviewOfSlug == "" || app.PreviewPrNumber <= 0 ||
			app.Status == AppDeleted || app.PreviewPrState == PreviewPrStateStale ||
			app.PreviewPrState == PreviewPrStateTearingDown || app.PreviewPrState == PreviewPrStateTornDown {
			continue
		}
		app.PreviewPrState = PreviewPrStateStale
		app.PreviewExpiresAt = &now
		m.apps[id] = app
		retired++
	}
	m.expirePullRequestBranchesLocked(accountID, projectID, now)
	return retired, nil
}

// expirePullRequestBranchesLocked ends a project's pull request branches
// now, as the PgStore retirements do. Callers hold m.mu.
func (m *MemStore) expirePullRequestBranchesLocked(accountID, projectID string, now time.Time) {
	if projectID == "" {
		return
	}
	for id, branch := range m.projectEnvironmentBranches {
		if branch.AccountID != accountID || branch.ProjectID != projectID || branch.PullRequestNumber <= 0 {
			continue
		}
		end := now
		if floor := branch.CreatedAt.Add(time.Second); end.Before(floor) {
			end = floor
		}
		if end.Before(branch.ExpiresAt) {
			branch.ExpiresAt = end
			m.projectEnvironmentBranches[id] = branch
		}
	}
}

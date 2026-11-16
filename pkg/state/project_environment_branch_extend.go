package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ADR-20261031193635 branch lifecycle: a branch's expiry can move.

// ErrProjectEnvironmentBranchDataPending refuses moving a branch's expiry
// while its data is still being copied: each database copy's TTL is fixed
// from the branch's expiry when the copy is made, and a retry must adopt
// the same copy.
var ErrProjectEnvironmentBranchDataPending = fmt.Errorf("%w: environment branch data is still being copied", ErrConflict)

// ProjectEnvironmentBranchExtender moves a branch's expiry.
type ProjectEnvironmentBranchExtender interface {
	// ExtendProjectEnvironmentBranch sets the branch's expiry. It refuses
	// a branch whose data is pending (ErrProjectEnvironmentBranchDataPending)
	// and an environment that is not a branch.
	ExtendProjectEnvironmentBranch(ctx context.Context, accountID, projectID, slug string, expiresAt time.Time) (ProjectEnvironmentBranch, error)
}

var (
	_ ProjectEnvironmentBranchExtender = (*MemStore)(nil)
	_ ProjectEnvironmentBranchExtender = (*PgStore)(nil)
)

func (m *MemStore) ExtendProjectEnvironmentBranch(_ context.Context, accountID, projectID, slug string, expiresAt time.Time) (ProjectEnvironmentBranch, error) {
	if expiresAt.IsZero() {
		return ProjectEnvironmentBranch{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	branch, err := m.projectEnvironmentBranchBySlugLocked(accountID, projectID, slug)
	if err != nil {
		return ProjectEnvironmentBranch{}, err
	}
	if branch.DataState == EnvironmentBranchDataPending {
		return ProjectEnvironmentBranch{}, ErrProjectEnvironmentBranchDataPending
	}
	if !expiresAt.After(branch.BranchPoint) {
		return ProjectEnvironmentBranch{}, ErrInvalidArgument
	}
	stored := m.projectEnvironmentBranches[branch.EnvironmentID]
	stored.ExpiresAt = expiresAt.UTC().Truncate(time.Microsecond)
	m.projectEnvironmentBranches[branch.EnvironmentID] = stored
	return m.branchViewLocked(stored), nil
}

func (s *PgStore) ExtendProjectEnvironmentBranch(ctx context.Context, accountID, projectID, slug string, expiresAt time.Time) (ProjectEnvironmentBranch, error) {
	if expiresAt.IsZero() {
		return ProjectEnvironmentBranch{}, ErrInvalidArgument
	}
	if _, err := uuid.Parse(accountID); err != nil {
		return ProjectEnvironmentBranch{}, ErrNotFound
	}
	if _, err := uuid.Parse(projectID); err != nil {
		return ProjectEnvironmentBranch{}, ErrNotFound
	}
	var environmentID string
	err := s.pool.QueryRow(ctx, `
		update project_environment_branches b
		   set expires_at = $4
		  from project_environments e
		 where e.id = b.environment_id and b.account_id = $1 and b.project_id = $2 and e.slug = $3
		   and b.data_state <> 'pending' and $4 > b.branch_point
		returning b.environment_id::text
	`, accountID, projectID, slug, expiresAt.UTC()).Scan(&environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Say why: not a branch, data pending, or an expiry before the
		// branch point.
		branch, lookupErr := s.ProjectEnvironmentBranchBySlug(ctx, accountID, projectID, slug)
		switch {
		case lookupErr != nil:
			return ProjectEnvironmentBranch{}, lookupErr
		case branch.DataState == EnvironmentBranchDataPending:
			return ProjectEnvironmentBranch{}, ErrProjectEnvironmentBranchDataPending
		default:
			return ProjectEnvironmentBranch{}, ErrInvalidArgument
		}
	}
	if err != nil {
		return ProjectEnvironmentBranch{}, mapErr(err)
	}
	return s.ProjectEnvironmentBranchBySlug(ctx, accountID, projectID, slug)
}

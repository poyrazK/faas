package apidserver

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const environmentBranchSweepInterval = time.Minute

// runEnvironmentBranchSweeper deletes environment branches past their expiry
// (ADR-20261031193635). It runs while branches are switched off too, so a
// branch created before the switch flipped still expires.
func (s *server) runEnvironmentBranchSweeper(ctx context.Context) {
	store, ok := s.store.(state.ProjectEnvironmentBranchStore)
	if !ok {
		return
	}
	ticker := time.NewTicker(environmentBranchSweepInterval)
	defer ticker.Stop()
	for {
		if err := s.sweepEnvironmentBranches(ctx, store, time.Now().UTC()); err != nil && ctx.Err() == nil && s.log != nil {
			s.log.Warn("environment branch sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sweepEnvironmentBranches deletes one batch of expired branches, deepest
// first, so a parent goes once its branches are gone.
func (s *server) sweepEnvironmentBranches(ctx context.Context, store state.ProjectEnvironmentBranchStore, now time.Time) error {
	expired, err := store.ExpiredProjectEnvironmentBranches(ctx, now, api.EnvironmentBranchSweepBatch)
	if err != nil {
		return err
	}
	var sweepErr error
	for _, branch := range expired {
		if err := ctx.Err(); err != nil {
			return errors.Join(sweepErr, err)
		}
		err := store.DeleteProjectEnvironmentBranch(ctx, branch.AccountID, branch.ProjectID, branch.Slug)
		if errors.Is(err, state.ErrNotFound) {
			continue // deleted by its owner meanwhile
		}
		if err != nil {
			sweepErr = errors.Join(sweepErr, err)
			continue
		}
		s.audit.Emit(ctx, "project.environment.expired", &branch.AccountID, map[string]any{
			"project_id": branch.ProjectID, "environment_id": branch.EnvironmentID, "environment_slug": branch.Slug,
			"expires_at": branch.ExpiresAt.Format(time.RFC3339), "branch": true,
		})
	}
	return sweepErr
}

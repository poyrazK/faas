package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) PreviewEnvironmentGitOpsCommit(ctx context.Context, expected EnvironmentGitSource, commit string, desired environmentsync.DesiredState) (api.PreviewEnvironmentGitPlanResponse, error) {
	if err := validateEnvironmentGitOpsPreview(expected, commit, desired); err != nil {
		return api.PreviewEnvironmentGitPlanResponse{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.PreviewEnvironmentGitPlanResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockEnvironmentGitSource(ctx, tx, sqlc.LockEnvironmentGitSourceParams{AccountID: mustPgUUID(expected.AccountID), SourceID: mustPgUUID(expected.ID)})
	if err != nil {
		return api.PreviewEnvironmentGitPlanResponse{}, mapErr(err)
	}
	scope, err := q.GetEnvironmentGitOpsScope(ctx, tx, row.ID)
	if err != nil {
		return api.PreviewEnvironmentGitPlanResponse{}, mapErr(err)
	}
	source := environmentGitSourceFromSQL(row, scope.EnvironmentSlug)
	if source.Detached {
		return api.PreviewEnvironmentGitPlanResponse{}, ErrNotFound
	}
	if source.Generation != expected.Generation || source.Spec != expected.Spec {
		return api.PreviewEnvironmentGitPlanResponse{}, ErrConflict
	}
	if desired.Definition.Project != scope.ProjectSlug || desired.Definition.Environment != source.EnvironmentSlug {
		return api.PreviewEnvironmentGitPlanResponse{}, ErrInvalidArgument
	}
	observation, _, err := readEnvironmentGitOpsIntent(ctx, tx, source, desired)
	if err != nil {
		return api.PreviewEnvironmentGitPlanResponse{}, err
	}
	return environmentGitOpsCommitPreview(source, commit, desired, observation)
}

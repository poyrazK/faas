package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ SourceImagePreparationStore = (*PgStore)(nil)

func (s *PgStore) PublishSourceImagePreparationLayer(ctx context.Context, input SourceBuildRootfsInput, token string) (SourceBuildRootfs, error) {
	in, hash, err := prepareSourceBuildRootfs(input)
	if err != nil {
		return SourceBuildRootfs{}, err
	}
	claim, err := parsePgUUID(token)
	if err != nil {
		return SourceBuildRootfs{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SourceBuildRootfs{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := sqlc.New().LockImagePreparationDeployment(ctx, tx, mustPgUUID(in.DeploymentID)); err != nil {
		return SourceBuildRootfs{}, mapErr(err)
	}
	if err := lockSourceBuildRootfsParents(ctx, tx, in); err != nil {
		return SourceBuildRootfs{}, err
	}
	value, err := saveSourceBuildRootfs(ctx, tx, in, hash)
	if err != nil {
		return SourceBuildRootfs{}, err
	}
	rows, err := sqlc.New().PublishImagePreparationLayer(ctx, tx, sqlc.PublishImagePreparationLayerParams{
		DeploymentID: mustPgUUID(in.DeploymentID), ClaimToken: claim, Path: in.RootfsPath, Key: in.StorageKey, Bytes: in.ContentBytes})
	if err != nil {
		return SourceBuildRootfs{}, err
	}
	if rows != 1 {
		return SourceBuildRootfs{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return SourceBuildRootfs{}, err
	}
	return value, nil
}

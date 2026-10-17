package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RegistryImagePreparationStore = (*PgStore)(nil)

func (s *PgStore) PublishRegistryImagePreparationLayer(ctx context.Context, input DeploymentRegistryRootfsInput, token string) (DeploymentRegistryRootfs, error) {
	in, hash, err := prepareRegistryRootfs(input)
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	if in.WorkloadName != "" {
		return DeploymentRegistryRootfs{}, ErrInvalidArgument
	}
	claim, err := parsePgUUID(token)
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := sqlc.New().LockImagePreparationDeployment(ctx, tx, mustPgUUID(in.DeploymentID)); err != nil {
		return DeploymentRegistryRootfs{}, mapErr(err)
	}
	value, err := publishDeploymentRegistryRootfsTx(ctx, tx, in, hash)
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	rows, err := sqlc.New().PublishImagePreparationLayer(ctx, tx, sqlc.PublishImagePreparationLayerParams{
		DeploymentID: mustPgUUID(in.DeploymentID), ClaimToken: claim, Path: in.RootfsPath, Key: in.StorageKey, Bytes: in.ContentBytes})
	if err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	if rows != 1 {
		return DeploymentRegistryRootfs{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return DeploymentRegistryRootfs{}, err
	}
	return value, nil
}
